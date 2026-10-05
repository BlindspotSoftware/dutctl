// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"io"
	"sync"
	"sync/atomic"
)

// crlfWriter writes to w and, while enabled, starts every line at column 0
// by turning a bare '\n' into "\r\n". A terminal in raw mode no longer does
// that itself, yet the client's own text, Print output, diagnostics and the
// epilogue, still shares the terminal with a raw console. Console bytes never
// pass through it: they go to the inner writer as they are.
//
// A "\r\n" split across two writes stays one: the writer remembers whether
// the previous write ended in CR. The formatter and the log handler share a
// writer, so writes are serialised. The switch is atomic and never waits for
// a write in progress: the console turns it off while it restores the
// terminal, which must not depend on a stream that blocks.
type crlfWriter struct {
	w  io.Writer
	on atomic.Bool

	mu     sync.Mutex // serialises writes and guards lastCR
	lastCR bool       // the previous write ended in CR, so a leading LF is not bare
}

// enable turns the conversion on or off.
func (c *crlfWriter) enable(on bool) {
	c.on.Store(on)
}

func (c *crlfWriter) Write(data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	prevCR := c.lastCR
	if len(data) > 0 {
		c.lastCR = data[len(data)-1] == '\r'
	}

	if !c.on.Load() || !bytes.ContainsRune(data, '\n') {
		return c.w.Write(data)
	}

	out := make([]byte, 0, len(data)+bytes.Count(data, []byte{'\n'}))

	for _, b := range data {
		if b == '\n' && !prevCR {
			out = append(out, '\r')
		}

		prevCR = b == '\r'

		out = append(out, b)
	}

	_, err := c.w.Write(out)
	if err != nil {
		return 0, err
	}

	return len(data), nil
}
