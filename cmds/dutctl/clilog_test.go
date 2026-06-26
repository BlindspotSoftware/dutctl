// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLogMode(t *testing.T) {
	tests := []struct {
		in      string
		want    logMode
		wantErr bool
	}{
		{"debug", logModeDebug, false},
		{"warn", logModeWarn, false},
		{"none", logModeNone, false},
		{"", 0, true},
		{"info", 0, true},
		{"bogus", 0, true},
	}

	for _, tt := range tests {
		got, err := parseLogMode(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseLogMode(%q): want error, got nil", tt.in)
			}

			continue
		}

		if err != nil {
			t.Errorf("parseLogMode(%q): unexpected error: %v", tt.in, err)
		}

		if got != tt.want {
			t.Errorf("parseLogMode(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// newTestLogger returns a logger backed by a cliHandler in the given mode,
// writing (colourless) to the returned buffer.
func newTestLogger(mode logMode) (*slog.Logger, *cliHandler, *bytes.Buffer) {
	var buf bytes.Buffer

	h := newCLIHandler(&buf, mode, false)

	return slog.New(h), h, &buf
}

func TestCLIHandler_WarnMode_BuffersUntilFlush(t *testing.T) {
	logger, handler, buf := newTestLogger(logModeWarn)

	logger.Debug("d1")          // dropped (debug tier)
	logger.Info("i1")           // dropped (Info < Warn)
	logger.Warn("w1", "k", "v") // buffered (warn tier)
	logger.Error("e1")          // buffered (Error >= Warn → warn tier)

	if buf.Len() != 0 {
		t.Fatalf("warn mode wrote before flush:\n%s", buf.String())
	}

	handler.Flush()

	out := buf.String()
	if !strings.Contains(out, "2 warning(s) during run:") {
		t.Errorf("missing summary header, got:\n%s", out)
	}

	for _, want := range []string{"- w1 k=v", "- e1"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q, got:\n%s", want, out)
		}
	}

	if strings.Contains(out, "d1") || strings.Contains(out, "i1") {
		t.Errorf("debug/info leaked into warn summary:\n%s", out)
	}
}

func TestCLIHandler_DebugMode_WritesLive(t *testing.T) {
	logger, handler, buf := newTestLogger(logModeDebug)

	logger.Debug("d1")
	logger.Warn("w1")

	out := buf.String()
	if !strings.Contains(out, "DEBUG d1") || !strings.Contains(out, "WARN w1") {
		t.Errorf("debug mode did not write live, got:\n%s", out)
	}

	before := buf.Len()
	handler.Flush() // nothing buffered in debug mode

	if buf.Len() != before {
		t.Errorf("Flush wrote in debug mode: %q", buf.String()[before:])
	}
}

func TestCLIHandler_NoneMode_Silent(t *testing.T) {
	logger, handler, buf := newTestLogger(logModeNone)

	logger.Debug("d1")
	logger.Warn("w1")
	logger.Error("e1")
	handler.Flush()

	if buf.Len() != 0 {
		t.Errorf("none mode produced output:\n%s", buf.String())
	}
}

func TestCLIHandler_FlushNoWarnings_NoOutput(t *testing.T) {
	_, handler, buf := newTestLogger(logModeWarn)

	handler.Flush()

	if buf.Len() != 0 {
		t.Errorf("Flush with no warnings produced output:\n%s", buf.String())
	}
}
