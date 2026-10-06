// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"io"
	"sync/atomic"
)

// crlfWriter writes to w and, while on is set, starts every line at column 0
// by turning a bare '\n' into "\r\n". A terminal in raw mode no longer does
// that itself, yet the client's own text, Print output, diagnostics and the
// epilogue, still shares the terminal with a raw console. Console bytes never
// pass through it: they go to the inner writer as they are.
type crlfWriter struct {
	w  io.Writer
	on atomic.Bool
}

func (c *crlfWriter) Write(data []byte) (int, error) {
	if !c.on.Load() || !bytes.ContainsRune(data, '\n') {
		return c.w.Write(data)
	}

	out := make([]byte, 0, len(data)+bytes.Count(data, []byte{'\n'}))

	for i, b := range data {
		if b == '\n' && (i == 0 || data[i-1] != '\r') {
			out = append(out, '\r')
		}

		out = append(out, b)
	}

	_, err := c.w.Write(out)
	if err != nil {
		return 0, err
	}

	return len(data), nil
}
