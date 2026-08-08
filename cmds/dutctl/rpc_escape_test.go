// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "testing"

// TestFilterEscapeSingleCall covers escape handling when the prefix and the
// following key arrive within one stdin read.
func TestFilterEscapeSingleCall(t *testing.T) {
	tests := []struct {
		name     string
		in       []byte
		wantOut  []byte
		wantQuit bool
	}{
		{
			name:    "plain bytes pass through",
			in:      []byte("hello"),
			wantOut: []byte("hello"),
		},
		{
			name:    "ctrl-c is forwarded to the DUT",
			in:      []byte{0x03},
			wantOut: []byte{0x03},
		},
		{
			name:    "ctrl-d is forwarded to the DUT",
			in:      []byte{0x04},
			wantOut: []byte{0x04},
		},
		{
			name:    "ctrl-z is forwarded to the DUT",
			in:      []byte{0x1a},
			wantOut: []byte{0x1a},
		},
		{
			name:     "ctrl-a then x quits",
			in:       []byte{escapePrefix, 'x'},
			wantOut:  []byte{},
			wantQuit: true,
		},
		{
			name:     "ctrl-a then X quits",
			in:       []byte{escapePrefix, 'X'},
			wantOut:  []byte{},
			wantQuit: true,
		},
		{
			name:     "ctrl-a then ctrl-x quits",
			in:       []byte{escapePrefix, escapeQuitCtrl},
			wantOut:  []byte{},
			wantQuit: true,
		},
		{
			name:    "ctrl-a ctrl-a sends one literal ctrl-a",
			in:      []byte{escapePrefix, escapePrefix},
			wantOut: []byte{escapePrefix},
		},
		{
			name:    "ctrl-a then other key forwards both",
			in:      []byte{escapePrefix, 'z'},
			wantOut: []byte{escapePrefix, 'z'},
		},
		{
			name:     "text before quit sequence is forwarded",
			in:       append([]byte("ab"), escapePrefix, 'x'),
			wantOut:  []byte("ab"),
			wantQuit: true,
		},
		{
			name:    "text around literal ctrl-a",
			in:      append(append([]byte("a"), escapePrefix, escapePrefix), 'b'),
			wantOut: append([]byte{'a', escapePrefix}, 'b'),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pending := false

			out, quit := filterEscape(tt.in, &pending)

			if string(out) != string(tt.wantOut) {
				t.Errorf("out = %q, want %q", out, tt.wantOut)
			}

			if quit != tt.wantQuit {
				t.Errorf("quit = %v, want %v", quit, tt.wantQuit)
			}

			if pending {
				t.Errorf("escapePending left true, want false")
			}
		})
	}
}

// TestFilterEscapeAcrossReads verifies the escape prefix and its following key
// are handled correctly when they arrive in separate stdin reads.
func TestFilterEscapeAcrossReads(t *testing.T) {
	pending := false

	// Read 1: lone prefix — nothing forwarded yet, state armed.
	out, quit := filterEscape([]byte{escapePrefix}, &pending)
	if len(out) != 0 || quit {
		t.Fatalf("read 1: out=%q quit=%v, want empty/false", out, quit)
	}

	if !pending {
		t.Fatal("read 1: escapePending = false, want true")
	}

	// Read 2: the quit key arrives separately.
	out, quit = filterEscape([]byte{'x'}, &pending)
	if !quit {
		t.Errorf("read 2: quit = false, want true")
	}

	if len(out) != 0 {
		t.Errorf("read 2: out = %q, want empty", out)
	}

	if pending {
		t.Errorf("read 2: escapePending = true, want false")
	}
}

// TestFilterEscapePrefixThenForward verifies an armed prefix followed by a
// normal byte in the next read forwards both bytes and disarms.
func TestFilterEscapePrefixThenForward(t *testing.T) {
	pending := false

	filterEscape([]byte{escapePrefix}, &pending) // arm

	out, quit := filterEscape([]byte{'k'}, &pending)
	if quit {
		t.Errorf("quit = true, want false")
	}

	if string(out) != string([]byte{escapePrefix, 'k'}) {
		t.Errorf("out = %q, want %q", out, []byte{escapePrefix, 'k'})
	}
}
