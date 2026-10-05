// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestEscapeFilter(t *testing.T) {
	tests := []struct {
		name  string
		reads []string // successive reads from the terminal
		want  []string // bytes forwarded per read
		quit  bool     // the quit sequence was seen on the last read
		echo  bool     // local echo is on afterwards
		shown string   // what the terminal received: echo and toggle lines
	}{
		{name: "plain keys", reads: []string{"ls\r"}, want: []string{"ls\r"}},
		{name: "control keys pass", reads: []string{"\x03\x04\x1a\x13\x1b[A"}, want: []string{"\x03\x04\x1a\x13\x1b[A"}},
		{name: "quit", reads: []string{"ab\x01x"}, want: []string{"ab"}, quit: true},
		{name: "quit uppercase", reads: []string{"\x01X"}, want: []string{""}, quit: true},
		{name: "quit ctrl-x", reads: []string{"\x01\x18"}, want: []string{""}, quit: true},
		{name: "bytes after quit are dropped", reads: []string{"\x01xmore"}, want: []string{""}, quit: true},
		{name: "prefix twice is a literal prefix", reads: []string{"\x01\x01"}, want: []string{"\x01"}},
		{name: "prefix and another key are both forwarded", reads: []string{"\x01z"}, want: []string{"\x01z"}},
		{name: "prefix split across reads", reads: []string{"ab\x01", "x"}, want: []string{"ab", ""}, quit: true},
		{name: "literal prefix split across reads", reads: []string{"\x01", "\x01c"}, want: []string{"", "\x01c"}},
		{
			name: "echo toggle on", reads: []string{"\x01e"}, want: []string{""}, echo: true,
			shown: "\r\n[dutctl] local echo on\r\n",
		},
		{
			name: "echo toggle twice is off", reads: []string{"\x01e", "\x01E"}, want: []string{"", ""},
			shown: "\r\n[dutctl] local echo on\r\n\r\n[dutctl] local echo off\r\n",
		},
		{
			name: "echo shows the keys with Enter as a new line", reads: []string{"\x01e", "ls\r"}, want: []string{"", "ls\r"},
			echo: true, shown: "\r\n[dutctl] local echo on\r\nls\r\n",
		},
		{
			name: "echo follows a toggle within one read", reads: []string{"ab\x01ecd\x01Eef"}, want: []string{"abcdef"},
			shown: "\r\n[dutctl] local echo on\r\ncd\r\n[dutctl] local echo off\r\n",
		},
		{
			name: "keys before a quit are echoed", reads: []string{"\x01e", "ab\x01x"}, want: []string{"", "ab"},
			quit: true, echo: true, shown: "\r\n[dutctl] local echo on\r\nab",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				term   bytes.Buffer
				filter = escapeFilter{term: &term}
				quit   bool
			)

			for i, in := range tt.reads {
				out, q := filter.apply([]byte(in))
				if !bytes.Equal(out, []byte(tt.want[i])) {
					t.Errorf("read %d: forwarded %q, want %q", i, out, tt.want[i])
				}

				quit = q
			}

			if quit != tt.quit {
				t.Errorf("quit = %v, want %v", quit, tt.quit)
			}

			if filter.echo != tt.echo {
				t.Errorf("echo = %v, want %v", filter.echo, tt.echo)
			}

			if got := term.String(); got != tt.shown {
				t.Errorf("terminal got %q, want %q", got, tt.shown)
			}
		})
	}
}

// Without the toggle nothing reaches the terminal, whatever is typed.
func TestEscapeFilterSilentWithoutEcho(t *testing.T) {
	var term bytes.Buffer

	filter := escapeFilter{term: &term}

	_, _ = filter.apply([]byte(strings.Repeat("x\r", 3)))

	if term.Len() != 0 {
		t.Errorf("terminal got %q without local echo", term.String())
	}
}
