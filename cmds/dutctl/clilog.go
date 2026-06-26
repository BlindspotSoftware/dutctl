// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/BlindspotSoftware/dutctl/internal/style"
)

// logMode controls how diagnostic log records are handled. It is derived from
// the --log flag and implements flag.Value, so an invalid --log value is
// rejected during flag parsing (flag.ExitOnError prints the error plus usage
// and exits) rather than being validated separately.
type logMode int

// Ensure implementing the flag.Value interface.
var _ flag.Value = (*logMode)(nil)

const (
	// logModeNone drops all diagnostics.
	logModeNone logMode = iota
	// logModeWarn drops debug records and accumulates warnings into a summary
	// that is flushed on termination (the default).
	logModeWarn
	// logModeDebug writes every record live to stderr in temporal order.
	logModeDebug
)

// parseLogMode maps a --log flag value to a logMode. Unknown values are
// rejected. The message is intentionally bare ("must be ...") because the flag
// package wraps it as `invalid value %q for flag -log: <msg>`.
func parseLogMode(s string) (logMode, error) {
	switch s {
	case "none":
		return logModeNone, nil
	case "warn":
		return logModeWarn, nil
	case "debug":
		return logModeDebug, nil
	default:
		return 0, errors.New("must be debug, warn, or none")
	}
}

// String renders the mode and provides the flag's default display.
func (m *logMode) String() string {
	switch *m {
	case logModeNone:
		return "none"
	case logModeDebug:
		return "debug"
	default:
		return "warn"
	}
}

// Set parses and stores the flag value, returning an error for unknown values.
func (m *logMode) Set(s string) error {
	mode, err := parseLogMode(s)
	if err != nil {
		return err
	}

	*m = mode

	return nil
}

// cliHandler is a slog.Handler tailored for an interactive CLI. It writes
// diagnostics to stderr only, and dispatches purely on the record's level so
// that any slog entry point (Debug/Info/Warn/Error/Log/...) is mapped into a
// two-channel model: everything < Warn is "debug tier", everything >= Warn is
// "warn tier".
//
// In warn mode, warn-tier records are accumulated and flushed as a summary on
// termination so they never interrupt command output. In debug mode every
// record is written live.
type cliHandler struct {
	w        io.Writer
	mode     logMode
	useColor bool
	mu       *sync.Mutex // shared across WithAttrs/WithGroup copies
	buf      *[]string   // accumulated warning lines, shared via pointer
	attrs    []slog.Attr
	groups   []string
}

// Ensure implementing the slog.Handler interface.
var _ slog.Handler = (*cliHandler)(nil)

// newCLIHandler creates a cliHandler writing to w.
func newCLIHandler(w io.Writer, mode logMode, useColor bool) *cliHandler {
	return &cliHandler{
		w:        w,
		mode:     mode,
		useColor: useColor,
		mu:       &sync.Mutex{},
		buf:      &[]string{},
	}
}

// Enabled reports whether a record at the given level should be handled.
func (h *cliHandler) Enabled(_ context.Context, level slog.Level) bool {
	switch h.mode {
	case logModeNone:
		return false
	case logModeWarn:
		return level >= slog.LevelWarn // drops Debug & Info (everything below Warn)
	default: // logModeDebug
		return true
	}
}

// Handle writes (debug mode) or buffers (warn mode) a record.
func (h *cliHandler) Handle(_ context.Context, rec slog.Record) error {
	line := h.render(rec)

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.mode == logModeDebug {
		fmt.Fprintln(h.w, line) // live, temporal order

		return nil
	}

	// logModeWarn: only warn-tier (>= Warn) records reach here; Enabled gates the rest.
	*h.buf = append(*h.buf, line)

	return nil
}

// WithAttrs returns a copy with attrs appended, sharing the buffer and mutex so
// warning accumulation stays global.
func (h *cliHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)

	return &clone
}

// WithGroup returns a copy that prefixes subsequent attribute keys with name.
func (h *cliHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	clone := *h
	clone.groups = append(append([]string{}, h.groups...), name)

	return &clone
}

// render formats a record as a single line: "[LEVEL ]msg key=value ...". The
// level prefix is only added in debug mode, where records of different levels
// are interleaved; in the warn summary the lines sit under a header so the
// prefix is redundant.
func (h *cliHandler) render(rec slog.Record) string {
	var builder strings.Builder

	if h.mode == logModeDebug {
		builder.WriteString(rec.Level.String())
		builder.WriteByte(' ')
	}

	builder.WriteString(rec.Message)

	prefix := ""
	if len(h.groups) > 0 {
		prefix = strings.Join(h.groups, ".") + "."
	}

	writeAttr := func(a slog.Attr) {
		builder.WriteByte(' ')
		builder.WriteString(prefix)
		builder.WriteString(a.Key)
		builder.WriteByte('=')
		builder.WriteString(a.Value.String())
	}

	for _, a := range h.attrs {
		writeAttr(a)
	}

	rec.Attrs(func(a slog.Attr) bool {
		writeAttr(a)

		return true
	})

	return builder.String()
}

// Flush writes the accumulated warning summary (warn mode) and clears the
// buffer. It is safe to call in any mode and when no warnings were recorded.
func (h *cliHandler) Flush() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(*h.buf) == 0 {
		return
	}

	header := fmt.Sprintf("%s %d warning(s) during run:", style.MarkerWarning, len(*h.buf))
	fmt.Fprintln(h.w, style.Colorize(h.useColor, style.Yellow, header))

	for _, line := range *h.buf {
		fmt.Fprintf(h.w, "  - %s\n", line)
	}

	*h.buf = (*h.buf)[:0]
}
