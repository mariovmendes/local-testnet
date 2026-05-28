package supervisor

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
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
const colorDim = "\033[2m"

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

func (s *Supervisor) startOne(ctx context.Context, spec ProcessSpec) error {
	logPath := filepath.Join(s.logDir, spec.Name+".log")
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	cmd := exec.CommandContext(ctx, spec.Binary, spec.Args...)

	// Build env: inherit host env then apply overrides.
	env := os.Environ()
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	// Pipe stdout+stderr through a line reader so we can tee to the log file
	// and to the terminal simultaneously.
	pr, pw, err := os.Pipe()
	if err != nil {
		lf.Close()
		return err
	}
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		lf.Close()
		return err
	}
	// Close the write end in the parent so the read end EOFs when the child exits.
	pw.Close()

	// Assign the next color from the palette.
	idx := colorIndex.Add(1) - 1
	color := palette[idx%uint32(len(palette))]

	p := &proc{spec: spec, cmd: cmd, done: make(chan error, 1), color: color}
	s.procs = append(s.procs, p)

	name := spec.Name
	nameLen := s.nameLen

	go func() {
		// Left-pad the name to nameLen for column alignment.
		prefix := fmt.Sprintf("%s%s%-*s%s ", color+colorBold, "", nameLen, name, colorReset)

		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			_, _ = fmt.Fprintln(lf, line)
			fmt.Printf("%s%s\n", prefix, line)
		}
		pr.Close()
		lf.Close()
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
