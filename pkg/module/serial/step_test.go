// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

import (
	"strings"
	"testing"
	"time"
)

func TestParseArgsSequence(t *testing.T) {
	cfg, err := parseArgs([]string{"-t", "30s", "--", "send", "foo", "expect", "bar", "send-raw", "x"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}

	if cfg.timeout != 30*time.Second {
		t.Errorf("timeout = %s, want 30s", cfg.timeout)
	}

	if len(cfg.steps) != 3 {
		t.Fatalf("len(steps) = %d, want 3", len(cfg.steps))
	}

	if cfg.steps[0].kind != stepSend || string(cfg.steps[0].payload) != "foo\r" {
		t.Errorf("step0 = %+v, want send %q", cfg.steps[0], "foo\\r")
	}

	if cfg.steps[1].kind != stepExpect || cfg.steps[1].expect == nil || !cfg.steps[1].expect.MatchString("xbarx") {
		t.Errorf("step1 = %+v, want expect matching 'bar'", cfg.steps[1])
	}

	if cfg.steps[2].kind != stepSendRaw || string(cfg.steps[2].payload) != "x" {
		t.Errorf("step2 = %+v, want send-raw %q (no EOL)", cfg.steps[2], "x")
	}
}

func TestParseArgsEOL(t *testing.T) {
	tests := []struct {
		eol  string
		want string
	}{
		{"cr", "foo\r"},
		{"lf", "foo\n"},
		{"crlf", "foo\r\n"},
		{"none", "foo"},
	}

	for _, tt := range tests {
		t.Run(tt.eol, func(t *testing.T) {
			cfg, err := parseArgs([]string{"-eol", tt.eol, "--", "send", "foo"})
			if err != nil {
				t.Fatalf("parseArgs: %v", err)
			}

			if got := string(cfg.steps[0].payload); got != tt.want {
				t.Errorf("payload = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseArgsDefaultEOLIsCR(t *testing.T) {
	cfg, err := parseArgs([]string{"--", "send", "foo"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}

	if got := string(cfg.steps[0].payload); got != "foo\r" {
		t.Errorf("default payload = %q, want %q (CR)", got, "foo\\r")
	}
}

func TestParseArgsErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"expect missing pattern", []string{"--", "expect"}},
		{"send missing data", []string{"--", "send"}},
		{"unknown verb", []string{"--", "frobnicate", "x"}},
		{"bad regex", []string{"--", "expect", "("}},
		{"bad eol", []string{"-eol", "wat", "--", "send", "x"}},
		{"bad escape", []string{"--", "send", `x\q`}},
		{"trailing backslash", []string{"--", "send", `x\`}},
		{"short hex escape", []string{"--", "send", `x\x4`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseArgs(tt.args); err == nil {
				t.Errorf("parseArgs(%q) = nil error, want error", tt.args)
			}
		})
	}
}

func TestDecodeEscapes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []byte
	}{
		{"plain", `hello`, []byte("hello")},
		{"named escapes", `a\r\n\tb`, []byte("a\r\n\tb")},
		{"backslash", `a\\b`, []byte(`a\b`)},
		{"hex", `\x41\x42`, []byte("AB")},
		{"ctrl-c", `\x03`, []byte{0x03}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeEscapes(tt.in)
			if err != nil {
				t.Fatalf("decodeEscapes(%q): %v", tt.in, err)
			}

			if string(got) != string(tt.want) {
				t.Errorf("decodeEscapes(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolveEOL(t *testing.T) {
	for _, tt := range []struct {
		name string
		want string
	}{
		{"cr", "\r"},
		{"CR", "\r"},
		{"lf", "\n"},
		{"crlf", "\r\n"},
		{"none", ""},
	} {
		got, err := resolveEOL(tt.name)
		if err != nil {
			t.Fatalf("resolveEOL(%q): %v", tt.name, err)
		}

		if string(got) != tt.want {
			t.Errorf("resolveEOL(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}

	if _, err := resolveEOL("nonsense"); err == nil {
		t.Error("resolveEOL(nonsense) = nil error, want error")
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"shorter than limit", "login:", 60, "login:"},
		{"exactly limit", "abcde", 5, "abcde"},
		{"longer than limit", "abcdefgh", 5, "abcde..."},
		{"multibyte not split", "héllo wörld", 5, "héllo..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncate(tt.in, tt.limit); got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
			}
		})
	}
}

func TestStepLabel(t *testing.T) {
	long := strings.Repeat("x", maxLabelLen+10)

	got := step{src: long}.label()
	if want := maxLabelLen + len("..."); len([]rune(got)) != want {
		t.Errorf("label rune length = %d, want %d (maxLabelLen + ellipsis)", len([]rune(got)), want)
	}

	if short := (step{src: "ok"}).label(); short != "ok" {
		t.Errorf("short label = %q, want %q (unchanged)", short, "ok")
	}
}

func TestParseArgsMonitor(t *testing.T) {
	// No step args selects monitor mode: zero steps, no error.
	cfg, err := parseArgs(nil)
	if err != nil {
		t.Fatalf("parseArgs(nil): %v", err)
	}

	if len(cfg.steps) != 0 {
		t.Errorf("steps = %d, want 0 (monitor mode)", len(cfg.steps))
	}

	// Flags without steps are still monitor mode, with the flag applied.
	cfg, err = parseArgs([]string{"-t", "5s"})
	if err != nil {
		t.Fatalf("parseArgs(-t 5s): %v", err)
	}

	if len(cfg.steps) != 0 || cfg.timeout != 5*time.Second {
		t.Errorf("got steps=%d timeout=%s, want 0 steps and 5s", len(cfg.steps), cfg.timeout)
	}
}

func TestParseArgsKeepEscapes(t *testing.T) {
	cfg, err := parseArgs([]string{"-keep-escapes", "--", "expect", "x"})
	if err != nil {
		t.Fatalf("parseArgs(-keep-escapes): %v", err)
	}

	if !cfg.keepEscapes {
		t.Error("keepEscapes = false, want true")
	}

	cfg, err = parseArgs([]string{"--", "expect", "x"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}

	if cfg.keepEscapes {
		t.Error("keepEscapes = true by default, want false")
	}
}
