// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package log provides structured logging for the dutagent and dutserver
// services, built on the standard library's log/slog.
//
// # Obtaining a logger
//
// A logger is carried through a request on its context.Context. Code retrieves
// it with FromContext and, at a component boundary, derives a child logger and
// stores it back with With (to add structured attributes) or WithScope (to set
// the scope). Because the context already flows through the agent's request
// path, a component can be given its logging context without changing call
// signatures, and each component then logs only its own concern while the
// surrounding attributes and scope are already attached.
//
// FromContext falls back to slog.Default(), so code that has no request context
// (process bootstrap, module Init/Deinit) still logs through the same backend.
//
// # Scope
//
// A scope identifies the component a log record originates from — the RPC layer,
// the session backend, a module, and so on. A scope is a single, flat label:
// setting a new scope replaces any previous one (scopes do not nest). Each
// component sets its own scope at its entry boundary.
//
// How the scope is rendered depends on the handler built by New: the text
// handler (human/TTY output) prepends it to the message as "[session] ...",
// while the JSON handler emits it as a "scope" attribute so it stays filterable.
package log

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
)

// ScopeKey is the attribute key under which the scope is emitted by the JSON
// handler.
const ScopeKey = "scope"

// ctxKey is the (unexported, collision-free) context key for the logger.
type ctxKey struct{}

// FromContext returns the logger stored in ctx, or slog.Default() if none is
// set. It never returns nil, so callers can log unconditionally.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}

	return slog.Default()
}

// Into returns a copy of ctx carrying l. Most call sites use With or WithScope
// instead; use Into directly to seed an externally built logger — for example a
// request logger in an interceptor, or a capturing logger in a test.
func Into(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// With returns a copy of ctx whose logger has the given attributes added. The
// arguments follow the slog key-value convention, e.g. With(ctx, "device", d).
func With(ctx context.Context, args ...any) context.Context {
	return Into(ctx, FromContext(ctx).With(args...))
}

// WithScope returns a copy of ctx whose logger is scoped to name. Setting a new
// scope replaces any previous one; see Scope and the package documentation.
func WithScope(ctx context.Context, name string) context.Context {
	return Into(ctx, Scope(FromContext(ctx), name))
}

// Scope returns a copy of l whose scope is set to name, replacing any existing
// scope. Use it when holding a *slog.Logger directly rather than a context;
// WithScope is the context-based equivalent used at component boundaries.
func Scope(l *slog.Logger, name string) *slog.Logger {
	if h, ok := l.Handler().(*scopeHandler); ok {
		return slog.New(&scopeHandler{inner: h.inner, scope: name, render: h.render})
	}

	return slog.New(&scopeHandler{inner: l.Handler(), scope: name})
}

// New builds a base logger writing to w. When json is false a text handler is
// used and the scope is rendered as a "[scope]" message prefix; when true a
// JSON handler is used and the scope is emitted as a ScopeKey attribute.
func New(w io.Writer, level slog.Leveler, json bool) *slog.Logger {
	if level == nil {
		level = slog.LevelInfo
	}

	if json {
		opts := &slog.HandlerOptions{Level: level}

		return slog.New(&scopeHandler{inner: slog.NewJSONHandler(w, opts), render: scopeAsAttr})
	}

	return slog.New(&scopeHandler{inner: newTextHandler(w, level, useColor(w)), render: scopeAsPrefix})
}

// useColor reports whether ANSI color should be emitted to w: only when w is a
// terminal (a character device) and the NO_COLOR convention is not set.
func useColor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}

	f, ok := w.(*os.File)
	if !ok {
		return false
	}

	info, err := f.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

// ParseLevel maps a level name (debug, info, warn, error; case-insensitive) to a
// slog.Level, returning LevelInfo for an empty or unrecognized value.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// scopeRender selects how a scope is rendered on a record.
type scopeRender int

const (
	scopeAsPrefix scopeRender = iota // text: "[scope] msg"
	scopeAsAttr                      // json: {"scope":"...", ...}
)

// scopeHandler wraps a slog.Handler and renders the current scope on each
// record. It is immutable: Scope, WithAttrs and WithGroup all return a fresh
// handler, so a logger is safe to share across concurrent requests.
type scopeHandler struct {
	inner  slog.Handler
	scope  string
	render scopeRender
}

func (h *scopeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *scopeHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.scope != "" {
		switch h.render {
		case scopeAsPrefix:
			r.Message = "[" + h.scope + "] " + r.Message
		case scopeAsAttr:
			r.AddAttrs(slog.String(ScopeKey, h.scope))
		}
	}

	return h.inner.Handle(ctx, r)
}

func (h *scopeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &scopeHandler{inner: h.inner.WithAttrs(attrs), scope: h.scope, render: h.render}
}

func (h *scopeHandler) WithGroup(name string) slog.Handler {
	return &scopeHandler{inner: h.inner.WithGroup(name), scope: h.scope, render: h.render}
}

// timeFormat is the timestamp layout for the human-readable text handler. It
// mirrors the standard library log package's default (date and clock, without
// sub-seconds or timezone).
const timeFormat = "2006/01/02 15:04:05"

// levelWidth is the column width the level name is padded to, so messages align
// regardless of level. It is the width of the longest standard name (DEBUG/ERROR).
const levelWidth = 5

// ANSI color codes used by the text handler when writing to a terminal.
const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiGray   = "\x1b[90m"
)

// levelColor returns the ANSI color for a level by severity, or "" for INFO,
// which stays in the terminal's default color (it is the common case).
func levelColor(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return ansiRed
	case l >= slog.LevelWarn:
		return ansiYellow
	case l < slog.LevelInfo:
		return ansiGray
	default:
		return ""
	}
}

// textHandler is a slog.Handler that writes compact, human-readable lines:
//
//	2006/01/02 15:04:05 LEVEL message key1=value1 key2=value2
//
// It is the non-JSON handler built by New. The scope is supplied by the
// wrapping scopeHandler as a "[scope] " message prefix, so it appears right
// after the level. The level is padded for alignment and, when color is set
// (w is a terminal), the timestamp is dimmed and the level colored by severity.
// Records are written under a shared mutex so concurrent goroutines never
// interleave partial lines.
type textHandler struct {
	mu    *sync.Mutex
	w     io.Writer
	level slog.Leveler
	color bool
	attrs string // preformatted " key=value" pairs carried from WithAttrs
	group string // dotted key prefix carried from WithGroup, e.g. "outer.inner."
}

func newTextHandler(w io.Writer, level slog.Leveler, color bool) *textHandler {
	return &textHandler{mu: &sync.Mutex{}, w: w, level: level, color: color}
}

func (h *textHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

func (h *textHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder

	// The timestamp is always dimmed (when color is on); it is metadata that
	// should recede on every line, regardless of level.
	if !r.Time.IsZero() {
		timestamp := r.Time.Format(timeFormat)

		if h.color {
			b.WriteString(ansiGray)
			b.WriteString(timestamp)
			b.WriteString(ansiReset)
		} else {
			b.WriteString(timestamp)
		}

		b.WriteByte(' ')
	}

	// The rest of the line (level, message and attributes) shares the level's
	// color, so the whole content is colored. INFO has no color and stays the
	// terminal default.
	color := ""
	if h.color {
		color = levelColor(r.Level)
	}

	if color != "" {
		b.WriteString(color)
	}

	levelText := r.Level.String()
	b.WriteString(levelText)

	for i := len(levelText); i < levelWidth; i++ {
		b.WriteByte(' ') // pad shorter level names to align the message column
	}

	b.WriteByte(' ')
	b.WriteString(r.Message)
	b.WriteString(h.attrs)

	r.Attrs(func(a slog.Attr) bool {
		appendAttr(&b, h.group, a)

		return true
	})

	if color != "" {
		b.WriteString(ansiReset)
	}

	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()

	_, err := io.WriteString(h.w, b.String())

	return err
}

func (h *textHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	var b strings.Builder

	b.WriteString(h.attrs)

	for _, a := range attrs {
		appendAttr(&b, h.group, a)
	}

	return &textHandler{mu: h.mu, w: h.w, level: h.level, color: h.color, attrs: b.String(), group: h.group}
}

func (h *textHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	return &textHandler{mu: h.mu, w: h.w, level: h.level, color: h.color, attrs: h.attrs, group: h.group + name + "."}
}

// appendAttr writes " key=value" for attr, prefixing the key with group and
// flattening nested groups. Empty attributes are skipped, matching slog's own
// text handler.
func appendAttr(b *strings.Builder, group string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()

	if attr.Equal(slog.Attr{}) {
		return
	}

	if attr.Value.Kind() == slog.KindGroup {
		attrs := attr.Value.Group()
		if len(attrs) == 0 {
			return
		}

		prefix := group
		if attr.Key != "" {
			prefix = group + attr.Key + "."
		}

		for _, ga := range attrs {
			appendAttr(b, prefix, ga)
		}

		return
	}

	b.WriteByte(' ')
	b.WriteString(group)
	b.WriteString(attr.Key)
	b.WriteByte('=')
	b.WriteString(quoteIfNeeded(attr.Value.String()))
}

// quoteIfNeeded wraps s in double quotes when it is empty or contains
// whitespace, '=' or control characters, so the value stays a single token.
func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}

	for _, r := range s {
		if r <= ' ' || r == '=' || r == '"' {
			return strconv.Quote(s)
		}
	}

	return s
}
