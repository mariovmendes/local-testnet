package supervisor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ethera-labs/local-testnet/internal/logger"
)

// ProcessSpec describes a native process to launch.
type ProcessSpec struct {
	Name   string
	Binary string
	Args   []string
	Env    map[string]string // merged on top of os.Environ()
}

// ANSI palette — one color per process, cycling if there are more than 8.
var palette = []string{
	"\033[36m", // cyan      — op-reth-a
	"\033[33m", // yellow    — op-reth-b
	"\033[35m", // magenta   — op-node-a
	"\033[34m", // blue      — op-node-b
	"\033[32m", // green     — op-batcher-a
	"\033[92m", // bright-green — op-batcher-b
	"\033[31m", // red       — op-proposer-a
	"\033[91m", // bright-red   — op-proposer-b
}

const colorReset = "\033[0m"
const colorBold = "\033[1m"

// colorIndex is a global counter so each new process gets the next color.
var colorIndex atomic.Uint32

type proc struct {
	spec  ProcessSpec
	cmd   *exec.Cmd
	done  chan error
	color string // ANSI prefix for this process
}

// Supervisor starts and supervises a set of native processes, streaming
// their combined stdout+stderr to per-process log files and to the terminal
// with per-process color coding.
type Supervisor struct {
	logDir  string
	procs   []*proc
	mu      sync.Mutex
	logger  *slog.Logger
	nameLen int // longest process name (for alignment)
}

// New creates a Supervisor that writes process logs under logDir.
func New(logDir string) *Supervisor {
	return &Supervisor{logDir: logDir, logger: logger.Named("supervisor")}
}

// Start launches every ProcessSpec in order. It returns on the first
// error; processes already started remain running.
func (s *Supervisor) Start(ctx context.Context, specs []ProcessSpec) error {
	if err := os.MkdirAll(s.logDir, 0o755); err != nil {
		return fmt.Errorf("failed to create log dir: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Compute alignment width from the longest name in this batch.
	for _, spec := range specs {
		if len(spec.Name) > s.nameLen {
			s.nameLen = len(spec.Name)
		}
	}

	for _, spec := range specs {
		if err := s.startOne(ctx, spec); err != nil {
			return fmt.Errorf("failed to start %s: %w", spec.Name, err)
		}
	}
	return nil
}

func (s *Supervisor) startOne(_ context.Context, spec ProcessSpec) error {
	logPath := filepath.Join(s.logDir, spec.Name+".log")
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	// Use context.Background() so the command is not cancelled when the deploy
	// binary exits. Processes are long-lived daemons; explicit cleanup is done
	// by stop_l2_procs in deploy.sh before each run.
	cmd := exec.CommandContext(context.Background(), spec.Binary, spec.Args...)

	// Build env: inherit host env then apply overrides.
	env := os.Environ()
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	// Write stdout/stderr directly to the log file (not through a pipe).
	// This is critical: when cmd.Stdout is an *os.File, Go dup2s it into the
	// child's fd 1/2 without creating an internal goroutine or pipe. The child
	// inherits its own copy of the fd and continues writing to the log file
	// even after the parent (deploy binary) exits.
	cmd.Stdout = lf
	cmd.Stderr = lf

	// Detach child into its own session so it is unaffected by signals sent
	// to the parent's process group (e.g. SIGHUP on terminal close).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		lf.Close()
		return err
	}
	// lf is held open by the child; the parent no longer needs it.
	lf.Close()

	// Assign the next color from the palette.
	idx := colorIndex.Add(1) - 1
	color := palette[idx%uint32(len(palette))]

	p := &proc{spec: spec, cmd: cmd, done: make(chan error, 1), color: color}
	s.procs = append(s.procs, p)

	name := spec.Name
	nameLen := s.nameLen

	// Tail the log file and display colored prefixed lines on the terminal.
	// This goroutine exists only for human-readable output during the deploy
	// phase; it dies when the parent exits but that does not affect the child.
	go func() {
		rf, err := os.Open(logPath)
		if err != nil {
			return
		}
		defer rf.Close()

		prefix := fmt.Sprintf("%s%-*s%s ", color+colorBold, nameLen, name, colorReset)
		reader := bufio.NewReaderSize(rf, 1<<20)
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				// Trim trailing newline for uniform printf formatting.
				if l := len(line); l > 0 && line[l-1] == '\n' {
					line = line[:l-1]
				}
				fmt.Printf("%s%s\n", prefix, line)
			}
			if err == io.EOF {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if err != nil {
				return
			}
		}
	}()

	go func() {
		p.done <- cmd.Wait()
	}()

	s.logger.Info("started process", "name", name, "pid", cmd.Process.Pid, "log", logPath)
	return nil
}

// Wait blocks until the first managed process exits (returning its error
// wrapped with the process name) or until ctx is cancelled (returning nil).
func (s *Supervisor) Wait(ctx context.Context) error {
	s.mu.Lock()
	procs := make([]*proc, len(s.procs))
	copy(procs, s.procs)
	s.mu.Unlock()

	for {
		// Non-blocking poll of every done channel.
		for i, p := range procs {
			select {
			case err := <-p.done:
				return fmt.Errorf("process %s exited: %w", procs[i].spec.Name, err)
			default:
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// StopAll sends SIGINT to every managed process, waits up to 5 seconds for
// each to exit, then force-kills any that remain.
func (s *Supervisor) StopAll(_ context.Context) {
	s.mu.Lock()
	procs := make([]*proc, len(s.procs))
	copy(procs, s.procs)
	s.mu.Unlock()

	for _, p := range procs {
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Signal(os.Interrupt)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for _, p := range procs {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			remaining = 0
		}
		t := time.NewTimer(remaining)
		select {
		case <-p.done:
		case <-t.C:
			if p.cmd.Process != nil {
				_ = p.cmd.Process.Kill()
			}
		}
		t.Stop()
	}
	s.logger.Info("all processes stopped")
}
