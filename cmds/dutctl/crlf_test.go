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
		name     string
		on       bool
		offFirst bool // the first write happens before the conversion is enabled
		writes   []string
		want     string
	}{
		{name: "off passes through", on: false, writes: []string{"a\nb\n"}, want: "a\nb\n"},
		{name: "on starts lines at column 0", on: true, writes: []string{"a\nb\n"}, want: "a\r\nb\r\n"},
		{name: "on keeps an existing CR LF", on: true, writes: []string{"a\r\nb"}, want: "a\r\nb"},
		{name: "on with a leading newline", on: true, writes: []string{"\nx"}, want: "\r\nx"},
		{name: "on without a newline", on: true, writes: []string{"prompt> "}, want: "prompt> "},
		{name: "on keeps a CR LF split across writes", on: true, writes: []string{"a\r", "\nb"}, want: "a\r\nb"},
		{name: "on converts a bare LF after an earlier CR", on: true, writes: []string{"a\r", "b\n"}, want: "a\rb\r\n"},
		{name: "a CR written before enabling counts", on: true, offFirst: true, writes: []string{"\r", "\n"}, want: "\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var inner bytes.Buffer

			w := &crlfWriter{w: &inner}

			for i, in := range tt.writes {
				w.enable(tt.on && (i > 0 || !tt.offFirst))

				n, err := w.Write([]byte(in))
				if err != nil {
					t.Fatalf("Write: %v", err)
				}

				if n != len(in) {
					t.Errorf("Write reported %d bytes, want %d (the caller's count)", n, len(in))
				}
			}

			if got := inner.String(); got != tt.want {
				t.Errorf("wrote %q, want %q", got, tt.want)
			}
		})
	}
}
