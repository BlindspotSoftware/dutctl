// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package log_test

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/BlindspotSoftware/dutctl/internal/log"
)

// newCtx returns a context carrying a logger that writes to buf, using New so
// the scope-rendering handler is exercised.
func newCtx(buf *bytes.Buffer, json bool) context.Context {
	return log.Into(context.Background(), log.New(buf, slog.LevelDebug, json))
}

func TestFromContextFallsBackToDefault(t *testing.T) {
	if got := log.FromContext(context.Background()); got != slog.Default() {
		t.Errorf("FromContext on empty context = %p, want slog.Default() %p", got, slog.Default())
	}
}

func TestTextScopeRendersAsPrefix(t *testing.T) {
	var buf bytes.Buffer
	ctx := newCtx(&buf, false)

	ctx = log.WithScope(ctx, "session")
	log.FromContext(ctx).Info("connected")

	if out := buf.String(); !strings.Contains(out, "[session] connected") {
		t.Errorf("text output = %q, want it to contain %q", out, "[session] connected")
	}
}

func TestJSONScopeRendersAsAttribute(t *testing.T) {
	var buf bytes.Buffer
	ctx := newCtx(&buf, true)

	ctx = log.WithScope(ctx, "session")
	log.FromContext(ctx).Info("connected")

	out := buf.String()
	if !strings.Contains(out, `"scope":"session"`) {
		t.Errorf("json output = %q, want it to contain %q", out, `"scope":"session"`)
	}
	if !strings.Contains(out, `"msg":"connected"`) {
		t.Errorf("json output = %q, want msg without a prefix", out)
	}
}

func TestNewScopeReplacesPrevious(t *testing.T) {
	var buf bytes.Buffer
	ctx := newCtx(&buf, false)

	ctx = log.WithScope(ctx, "rpc")
	ctx = log.WithScope(ctx, "session") // replaces "rpc", does not nest
	log.FromContext(ctx).Info("hello")

	out := buf.String()
	if !strings.Contains(out, "[session] hello") {
		t.Errorf("output = %q, want it to contain %q", out, "[session] hello")
	}
	if strings.Contains(out, "rpc") {
		t.Errorf("output = %q, want previous scope %q to be replaced", out, "rpc")
	}
}

func TestWithAttributesSurviveScope(t *testing.T) {
	var buf bytes.Buffer
	ctx := newCtx(&buf, false)

	ctx = log.With(ctx, "device", "dut-1")
	ctx = log.WithScope(ctx, "module") // scope change must keep earlier attributes
	log.FromContext(ctx).Info("running")

	out := buf.String()
	if !strings.Contains(out, "[module] running") {
		t.Errorf("output = %q, want scope prefix", out)
	}
	if !strings.Contains(out, "device=dut-1") {
		t.Errorf("output = %q, want attribute device=dut-1 to survive the scope change", out)
	}
}

func TestTextHandlerFormat(t *testing.T) {
	var buf bytes.Buffer
	ctx := newCtx(&buf, false)

	ctx = log.WithScope(ctx, "svc")
	log.FromContext(ctx).Info("hello there", "code", 7, "note", "with space")

	out := strings.TrimRight(buf.String(), "\n")

	// Shape: "2006/01/02 15:04:05 INFO  [svc] hello there code=7 note=..."
	// (INFO is padded to width 5, so two spaces precede the scope.) A plain
	// bytes.Buffer is not a terminal, so no color codes are emitted.
	want := regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} INFO {2}\[svc\] hello there code=7 note="with space"$`)
	if !want.MatchString(out) {
		t.Errorf("text line = %q, want it to match %v", out, want)
	}

	// The compact format must not carry slog's machine-style time=/level=/msg= keys.
	for _, bad := range []string{"time=", "level=", "msg="} {
		if strings.Contains(out, bad) {
			t.Errorf("text line = %q, must not contain %q", out, bad)
		}
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"INFO":    slog.LevelInfo,
		"Warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"":        slog.LevelInfo,
		"bogus":   slog.LevelInfo,
	}
	for in, want := range cases {
		if got := log.ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}
