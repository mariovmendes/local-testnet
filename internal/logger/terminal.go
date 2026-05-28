package logger

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// terminalHandler is a slog.Handler that writes human-readable, colored log
// lines to a terminal instead of raw JSON.
//
// Format:
//
//	HH:MM:SS  LEVEL  [name]  message  key=value …
type terminalHandler struct {
	w     io.Writer
	level slog.Level
	attrs []slog.Attr
	group string
}

const (
	tReset  = "\033[0m"
	tBold   = "\033[1m"
	tDim    = "\033[2m"
	tRed    = "\033[31m"
	tGreen  = "\033[32m"
	tYellow = "\033[33m"
	tCyan   = "\033[36m"
	tWhite  = "\033[97m"
)

// newTerminalHandler creates a terminalHandler that writes to w.
func newTerminalHandler(w io.Writer, level slog.Level) *terminalHandler {
	return &terminalHandler{w: w, level: level}
}

func (h *terminalHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *terminalHandler) Handle(_ context.Context, r slog.Record) error {
	var b bytes.Buffer

	// Timestamp — short HH:MM:SS
	b.WriteString(tDim)
	b.WriteString(r.Time.Format(time.TimeOnly))
	b.WriteString(tReset)
	b.WriteString("  ")

	// Level
	switch r.Level {
	case slog.LevelError:
		b.WriteString(tBold + tRed + "ERROR" + tReset)
	case slog.LevelWarn:
		b.WriteString(tBold + tYellow + " WARN" + tReset)
	case slog.LevelInfo:
		b.WriteString(tCyan + " INFO" + tReset)
	default:
		b.WriteString(tDim + "DEBUG" + tReset)
	}
	b.WriteString("  ")

	// Named logger (from "name" attribute injected by logger.Named)
	name := ""
	for _, a := range h.attrs {
		if a.Key == "name" {
			name = a.Value.String()
		}
	}

	// Collect record attributes; pick up "name" and skip noisy "config" dumps.
	var kvParts []string
	skip := map[string]bool{"name": true, "config": true}
	r.Attrs(func(a slog.Attr) bool {
		if skip[a.Key] {
			if a.Key == "name" {
				name = a.Value.String()
			}
			return true
		}
		v := fmt.Sprintf("%v", a.Value.Any())
		// Truncate very long values (e.g. full paths, addresses).
		if len(v) > 120 {
			v = v[:117] + "…"
		}
		kvParts = append(kvParts, tDim+a.Key+"="+tReset+tDim+v+tReset)
		return true
	})

	// [name] prefix when available.
	if name != "" {
		b.WriteString(tDim + "[" + name + "]" + tReset + "  ")
	}

	// Message — bold for errors/warnings, plain otherwise.
	msg := r.Message
	switch {
	case strings.HasPrefix(msg, "starting l2 command"):
		msg = "validating L2 config"
	case msg == "repository already cloned, skipping":
		return nil // suppress per-repo noise; "all repositories cloned" covers it
	case msg == "ignoring op-succinct repository config because op-succinct mode is disabled":
		return nil // always disabled, never interesting
	}
	switch r.Level {
	case slog.LevelError:
		b.WriteString(tBold + tRed + msg + tReset)
	case slog.LevelWarn:
		b.WriteString(tYellow + msg + tReset)
	default:
		b.WriteString(tWhite + msg + tReset)
	}

	// Key-value pairs.
	if len(kvParts) > 0 {
		b.WriteString("  " + strings.Join(kvParts, "  "))
	}

	b.WriteByte('\n')
	_, err := h.w.Write(b.Bytes())
	return err
}

func (h *terminalHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	n := *h
	n.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &n
}

func (h *terminalHandler) WithGroup(name string) slog.Handler {
	n := *h
	n.group = name
	return &n
}

// InitializeTerminal replaces the global slog logger with a pretty-printing
// terminal handler. Call this for commands that write their own UI.
func InitializeTerminal(level slog.Level) {
	slog.SetDefault(slog.New(newTerminalHandler(os.Stdout, level)))
}
