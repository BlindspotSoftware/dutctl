// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
)

func TestEscapeFilter(t *testing.T) {
	tests := []struct {
		name    string
		reads   []string // successive reads from the terminal
		want    []string // bytes forwarded per read
		quit    bool     // the quit sequence was seen on the last read
		echo    bool     // local echo is on afterwards
		toggled int      // reads on which the echo toggle was hit
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
		{name: "echo toggle on", reads: []string{"\x01e"}, want: []string{""}, echo: true, toggled: 1},
		{name: "echo toggle twice is off", reads: []string{"\x01e", "\x01E"}, want: []string{"", ""}, toggled: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				filter  escapeFilter
				quit    bool
				toggled int
			)

			for i, in := range tt.reads {
				out, q, tog := filter.apply([]byte(in))
				if !bytes.Equal(out, []byte(tt.want[i])) {
					t.Errorf("read %d: forwarded %q, want %q", i, out, tt.want[i])
				}

				quit = q

				if tog {
					toggled++
				}
			}

			if quit != tt.quit {
				t.Errorf("quit = %v, want %v", quit, tt.quit)
			}

			if filter.echo != tt.echo {
				t.Errorf("echo = %v, want %v", filter.echo, tt.echo)
			}

			if toggled != tt.toggled {
				t.Errorf("toggled %d times, want %d", toggled, tt.toggled)
			}
		})
	}
}

func TestLocalEcho(t *testing.T) {
	var out bytes.Buffer

	localEcho(&out, []byte("ls\rpwd\r"))

	if got, want := out.String(), "ls\r\npwd\r\n"; got != want {
		t.Errorf("echoed %q, want %q", got, want)
	}
}
