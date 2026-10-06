// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
)

func TestCRLFWriter(t *testing.T) {
	tests := []struct {
		name string
		on   bool
		in   string
		want string
	}{
		{name: "off passes through", on: false, in: "a\nb\n", want: "a\nb\n"},
		{name: "on starts lines at column 0", on: true, in: "a\nb\n", want: "a\r\nb\r\n"},
		{name: "on keeps an existing CR LF", on: true, in: "a\r\nb", want: "a\r\nb"},
		{name: "on with a leading newline", on: true, in: "\nx", want: "\r\nx"},
		{name: "on without a newline", on: true, in: "prompt> ", want: "prompt> "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var inner bytes.Buffer

			w := &crlfWriter{w: &inner}
			w.on.Store(tt.on)

			n, err := w.Write([]byte(tt.in))
			if err != nil {
				t.Fatalf("Write: %v", err)
			}

			if n != len(tt.in) {
				t.Errorf("Write reported %d bytes, want %d (the caller's count)", n, len(tt.in))
			}

			if got := inner.String(); got != tt.want {
				t.Errorf("wrote %q, want %q", got, tt.want)
			}
		})
	}
}
