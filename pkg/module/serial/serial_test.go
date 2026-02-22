// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

import (
	"strings"
	"testing"
	"time"
)

// TestFilterOutputCSI verifies that filterOutputCSI correctly passes through
// plain text and SGR sequences, drops all other CSI sequences, and stores
// incomplete sequences in the remainder for the next read.
//
//nolint:funlen
func TestFilterOutputCSI(t *testing.T) {
	tests := []struct {
		name          string
		input         []byte
		wantOutput    []byte
		wantRemainder []byte
	}{
		{
			name:          "empty input",
			input:         []byte{},
			wantOutput:    []byte{},
			wantRemainder: nil,
		},
		{
			name:          "plain text without ESC",
			input:         []byte("hello world"),
			wantOutput:    []byte("hello world"),
			wantRemainder: nil,
		},
		{
			name:          "SGR reset ESC[m preserved",
			input:         []byte("\x1b[m"),
			wantOutput:    []byte("\x1b[m"),
			wantRemainder: nil,
		},
		{
			name:          "SGR foreground colour ESC[31m preserved",
			input:         []byte("\x1b[31m"),
			wantOutput:    []byte("\x1b[31m"),
			wantRemainder: nil,
		},
		{
			name:          "SGR multi-parameter ESC[0;31m preserved",
			input:         []byte("\x1b[0;31m"),
			wantOutput:    []byte("\x1b[0;31m"),
			wantRemainder: nil,
		},
		{
			name:          "DSR query ESC[6n dropped",
			input:         []byte("\x1b[6n"),
			wantOutput:    []byte{},
			wantRemainder: nil,
		},
		{
			name:          "cursor position report ESC[10;20R dropped",
			input:         []byte("\x1b[10;20R"),
			wantOutput:    []byte{},
			wantRemainder: nil,
		},
		{
			name:          "cursor-up ESC[2A dropped",
			input:         []byte("\x1b[2A"),
			wantOutput:    []byte{},
			wantRemainder: nil,
		},
		{
			name:          "erase-display ESC[2J dropped",
			input:         []byte("\x1b[2J"),
			wantOutput:    []byte{},
			wantRemainder: nil,
		},
		{
			name:          "text surrounding SGR preserved",
			input:         []byte("hello \x1b[31mworld"),
			wantOutput:    []byte("hello \x1b[31mworld"),
			wantRemainder: nil,
		},
		{
			name:          "non-SGR CSI in middle of text dropped",
			input:         []byte("hello \x1b[6n world"),
			wantOutput:    []byte("hello  world"),
			wantRemainder: nil,
		},
		{
			name:          "mixed SGR and non-SGR: only SGR kept",
			input:         []byte("\x1b[31mcolor\x1b[6nquery\x1b[0mreset"),
			wantOutput:    []byte("\x1b[31mcolorquery\x1b[0mreset"),
			wantRemainder: nil,
		},
		{
			name:          "multiple consecutive SGR sequences preserved",
			input:         []byte("\x1b[31mred\x1b[0mreset"),
			wantOutput:    []byte("\x1b[31mred\x1b[0mreset"),
			wantRemainder: nil,
		},
		{
			name:          "lone ESC at end of buffer stored in remainder",
			input:         []byte("text\x1b"),
			wantOutput:    []byte("text"),
			wantRemainder: []byte{escByte},
		},
		{
			name:          "incomplete CSI — ESC[ only — stored in remainder",
			input:         []byte("\x1b["),
			wantOutput:    []byte{},
			wantRemainder: []byte("\x1b["),
		},
		{
			name:          "incomplete CSI with params stored in remainder",
			input:         []byte("text\x1b[31"),
			wantOutput:    []byte("text"),
			wantRemainder: []byte("\x1b[31"),
		},
		{
			name:          "ESC not followed by bracket emitted as-is",
			input:         []byte("\x1bO"), // SS3 — not a CSI sequence
			wantOutput:    []byte("\x1bO"),
			wantRemainder: nil,
		},
		{
			name:          "ESC not followed by bracket in middle of text",
			input:         []byte("ab\x1bOcd"),
			wantOutput:    []byte("ab\x1bOcd"),
			wantRemainder: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var remainder []byte

			got := filterOutputCSI(tt.input, &remainder)

			if string(got) != string(tt.wantOutput) {
				t.Errorf("output = %q, want %q", got, tt.wantOutput)
			}

			if string(remainder) != string(tt.wantRemainder) {
				t.Errorf("remainder = %q, want %q", remainder, tt.wantRemainder)
			}
		})
	}
}

// TestFilterOutputCSISequenceSplitAcrossReads simulates an SGR sequence
// whose bytes arrive in two separate buffer reads, verifying that the
// remainder mechanism reconstitutes it correctly.
func TestFilterOutputCSISequenceSplitAcrossReads(t *testing.T) {
	var remainder []byte

	// Read 1: "ESC[31" — missing the final byte 'm'.
	out1 := filterOutputCSI([]byte("\x1b[31"), &remainder)

	if len(out1) != 0 {
		t.Errorf("read 1: expected no output for incomplete sequence, got %q", out1)
	}

	if string(remainder) != "\x1b[31" {
		t.Errorf("read 1: remainder = %q, want %q", remainder, "\x1b[31")
	}

	// Read 2: prepend remainder to the new chunk (mirroring the main loop).
	chunk := append(remainder, 'm')
	out2 := filterOutputCSI(chunk, &remainder)

	if string(out2) != "\x1b[31m" {
		t.Errorf("read 2: output = %q, want %q", out2, "\x1b[31m")
	}

	if len(remainder) != 0 {
		t.Errorf("read 2: expected empty remainder, got %q", remainder)
	}
}

// TestFilterOutputCSILoneESCSplitAcrossReads simulates a lone ESC at the end
// of one read whose '[' and final byte arrive in the next read.
func TestFilterOutputCSILoneESCSplitAcrossReads(t *testing.T) {
	var remainder []byte

	// Read 1: text followed by a lone ESC at the buffer boundary.
	out1 := filterOutputCSI([]byte("text\x1b"), &remainder)

	if string(out1) != "text" {
		t.Errorf("read 1: output = %q, want %q", out1, "text")
	}

	if string(remainder) != "\x1b" {
		t.Errorf("read 1: remainder = %q, want %q", remainder, "\x1b")
	}

	// Read 2: the bytes "[A" arrive — together with the remainder this forms the
	// cursor-up sequence ESC[A which must be dropped.
	chunk := append(remainder, []byte("[A")...)
	out2 := filterOutputCSI(chunk, &remainder)

	if len(out2) != 0 {
		t.Errorf("read 2: cursor-up sequence should be dropped, got %q", out2)
	}

	if len(remainder) != 0 {
		t.Errorf("read 2: expected empty remainder, got %q", remainder)
	}
}

// TestEvalArgs covers argument parsing for the serial module.
func TestEvalArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantTimeout time.Duration
		wantPattern string // empty means expect should be nil
		wantErr     bool
	}{
		{
			name: "no args",
			args: nil,
		},
		{
			name:        "timeout flag only",
			args:        []string{"-t", "5s"},
			wantTimeout: 5 * time.Second,
		},
		{
			name:        "pattern only",
			args:        []string{"login:"},
			wantPattern: "login:",
		},
		{
			name:        "timeout and pattern",
			args:        []string{"-t", "2m", "hello world"},
			wantTimeout: 2 * time.Minute,
			wantPattern: "hello world",
		},
		{
			name:        "regex pattern with flags",
			args:        []string{`(?i)Login\s*:`},
			wantPattern: `(?i)Login\s*:`,
		},
		{
			name:    "invalid regex",
			args:    []string{"[invalid"},
			wantErr: true,
		},
		{
			name:    "unknown flag",
			args:    []string{"-x"},
			wantErr: true,
		},
		{
			name:    "invalid timeout value",
			args:    []string{"-t", "not-a-duration"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Serial{}

			err := s.evalArgs(tt.args)

			if (err != nil) != tt.wantErr {
				t.Fatalf("evalArgs() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			if s.timeout != tt.wantTimeout {
				t.Errorf("timeout = %v, want %v", s.timeout, tt.wantTimeout)
			}

			if tt.wantPattern == "" {
				if s.expect != nil {
					t.Errorf("expect = %v, want nil", s.expect)
				}
			} else {
				if s.expect == nil {
					t.Fatalf("expect is nil, want pattern %q", tt.wantPattern)
				}

				if s.expect.String() != tt.wantPattern {
					t.Errorf("expect pattern = %q, want %q", s.expect.String(), tt.wantPattern)
				}
			}
		})
	}
}

// TestSerialInit covers Init validation and default-baud-rate assignment.
func TestSerialInit(t *testing.T) {
	tests := []struct {
		name     string
		serial   Serial
		wantBaud int
		wantErr  bool
	}{
		{
			name:    "missing port returns error",
			serial:  Serial{},
			wantErr: true,
		},
		{
			name:     "zero baud is replaced with DefaultBaudRate",
			serial:   Serial{Port: "/dev/ttyS0"},
			wantBaud: DefaultBaudRate,
		},
		{
			name:     "explicit baud rate is preserved",
			serial:   Serial{Port: "/dev/ttyS0", Baud: 9600},
			wantBaud: 9600,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.serial.Init()

			if (err != nil) != tt.wantErr {
				t.Fatalf("Init() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr && tt.serial.Baud != tt.wantBaud {
				t.Errorf("Baud = %d, want %d", tt.serial.Baud, tt.wantBaud)
			}
		})
	}
}

// TestSerialHelp verifies that the help text includes the configured port,
// baud rate, and key usage information.
func TestSerialHelp(t *testing.T) {
	tests := []struct {
		name    string
		serial  Serial
		wantIn  []string
	}{
		{
			name:   "contains configured port and baud",
			serial: Serial{Port: "/dev/ttyS0", Baud: 9600},
			wantIn: []string{"/dev/ttyS0", "9600"},
		},
		{
			name:   "contains timeout flag and regex mention",
			serial: Serial{Port: "/dev/ttyUSB0", Baud: DefaultBaudRate},
			wantIn: []string{"-t", "expect", "regex"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			help := strings.ToLower(tt.serial.Help())

			for _, want := range tt.wantIn {
				if !strings.Contains(help, strings.ToLower(want)) {
					t.Errorf("Help() missing %q", want)
				}
			}
		})
	}
}
