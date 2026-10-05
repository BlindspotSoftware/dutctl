// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"io"
)

// The local escape sequence of a raw console, in the convention of screen and
// picocom: Ctrl-A is the prefix, and the key after it decides. Ctrl-A x (or X,
// or Ctrl-X) ends the session; Ctrl-A twice sends a single Ctrl-A; Ctrl-A e
// (or E) toggles local echo, for a far end that does not echo; any other key
// is forwarded together with the prefix. Every other byte, Ctrl-C included,
// goes to the far end as it is: with the terminal raw, the escape sequence is
// the one way to end the session from the keyboard.
const (
	escapePrefix   = 0x01 // Ctrl-A
	escapeQuitCtrl = 0x18 // Ctrl-X
)

// escapeFilter applies the escape sequence to the bytes read from a raw
// terminal and shows them on the terminal while local echo is on. The prefix
// and its key may arrive in separate reads, so a pending prefix is carried
// across calls.
type escapeFilter struct {
	pending bool
	echo    bool      // local echo is on
	term    io.Writer // the terminal the echo and the toggle's state line go to
}

// apply returns the bytes of in to forward and whether the quit key was seen
// (the bytes after it are dropped). The forwarded bytes are echoed in input
// order, so a toggle takes effect from the key after it, also within one
// read.
func (f *escapeFilter) apply(in []byte) ([]byte, bool) {
	out := make([]byte, 0, len(in))
	shown := 0 // out[:shown] went through show already

	for _, b := range in {
		if f.pending {
			f.pending = false

			switch b {
			case 'x', 'X', escapeQuitCtrl:
				f.show(out[shown:])

				return out, true
			case escapePrefix:
				out = append(out, escapePrefix)
			case 'e', 'E':
				f.show(out[shown:])
				shown = len(out)

				f.toggleEcho()
			default:
				out = append(out, escapePrefix, b)
			}

			continue
		}

		if b == escapePrefix {
			f.pending = true

			continue
		}

		out = append(out, b)
	}

	f.show(out[shown:])

	return out, false
}

// toggleEcho switches local echo and says so on the terminal.
func (f *escapeFilter) toggleEcho() {
	f.echo = !f.echo

	state := "off"
	if f.echo {
		state = "on"
	}

	fmt.Fprintf(f.term, "\r\n[dutctl] local echo %s\r\n", state)
}

// show echoes keys on the terminal while local echo is on, for a far end
// that does not echo them; Enter, which arrives as CR, moves to the next line.
func (f *escapeFilter) show(keys []byte) {
	if !f.echo || len(keys) == 0 {
		return
	}

	_, _ = f.term.Write(bytes.ReplaceAll(keys, []byte{'\r'}, []byte("\r\n")))
}
