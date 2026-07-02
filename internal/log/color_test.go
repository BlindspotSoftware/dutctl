// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package log

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// emit logs one record through a text handler with the given color setting and
// returns the written line. White-box so it can set color without a real TTY.
func emit(color bool, level slog.Level, msg string) string {
	var buf bytes.Buffer

	h := newTextHandler(&buf, slog.LevelDebug, color)
	rec := slog.NewRecord(time.Time{}, level, msg, 0) //nolint:exhaustruct

	_ = h.Handle(context.Background(), rec)

	return buf.String()
}

func TestColorOnlyWhenEnabled(t *testing.T) {
	if got := emit(false, slog.LevelError, "boom"); strings.Contains(got, "\x1b[") {
		t.Errorf("color disabled, but line has ANSI codes: %q", got)
	}

	got := emit(true, slog.LevelError, "boom")
	if !strings.Contains(got, ansiRed) {
		t.Errorf("color enabled for ERROR, want red %q in %q", ansiRed, got)
	}
	if !strings.Contains(got, ansiReset) {
		t.Errorf("colored line must reset, missing %q in %q", ansiReset, got)
	}
}

func TestLevelColorBySeverity(t *testing.T) {
	cases := map[slog.Level]string{
		slog.LevelDebug: ansiGray,
		slog.LevelWarn:  ansiYellow,
		slog.LevelError: ansiRed,
	}
	for level, want := range cases {
		if got := emit(true, level, "x"); !strings.Contains(got, want) {
			t.Errorf("level %v: want color %q in %q", level, want, got)
		}
	}

	// INFO stays the terminal default. emit uses a zero timestamp (no dimmed
	// time prefix), so an uncolored INFO line carries no ANSI codes at all.
	got := emit(true, slog.LevelInfo, "x")
	if strings.Contains(got, "\x1b[") {
		t.Errorf("INFO level must be uncolored, got %q", got)
	}
}

func TestColorSpansWholeContent(t *testing.T) {
	// For a colored level the message (not just the level token) is inside the
	// colored span, i.e. the reset comes after the message.
	got := emit(true, slog.LevelError, "boom")

	start := strings.Index(got, ansiRed)
	msg := strings.Index(got, "boom")
	reset := strings.LastIndex(got, ansiReset)

	if start < 0 || msg < 0 || reset < 0 || !(start < msg && msg < reset) {
		t.Errorf("want red ... boom ... reset in order, got %q", got)
	}
}

func TestLevelPadding(t *testing.T) {
	// INFO (4 chars) padded to width 5 then a separator space => two spaces.
	if got := emit(false, slog.LevelInfo, "m"); !strings.Contains(got, "INFO  m") {
		t.Errorf("want padded %q in %q", "INFO  m", got)
	}
	// ERROR (5 chars) needs no padding, just the separator space.
	if got := emit(false, slog.LevelError, "m"); !strings.Contains(got, "ERROR m") {
		t.Errorf("want %q in %q", "ERROR m", got)
	}
}
