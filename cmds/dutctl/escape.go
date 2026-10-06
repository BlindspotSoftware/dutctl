// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
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
// terminal. The prefix and its key may arrive in separate reads, so a pending
// prefix is carried across calls. echo is the state of local echo.
type escapeFilter struct {
	pending bool
	echo    bool
}

// apply returns the bytes of in to forward, whether the quit key was seen (the
// bytes after it are dropped) and whether the echo toggle was hit.
func (f *escapeFilter) apply(in []byte) ([]byte, bool, bool) {
	out := make([]byte, 0, len(in))
	toggled := false

	for _, b := range in {
		if f.pending {
			f.pending = false

			switch b {
			case 'x', 'X', escapeQuitCtrl:
				return out, true, toggled
			case escapePrefix:
				out = append(out, escapePrefix)
			case 'e', 'E':
				f.echo = !f.echo
				toggled = true
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

	return out, false, toggled
}

// localEcho shows the user's own keys on the raw terminal, for a far end that
// does not echo them; Enter, which arrives as CR, moves to the next line.
func localEcho(w io.Writer, keys []byte) {
	_, _ = w.Write(bytes.ReplaceAll(keys, []byte{'\r'}, []byte("\r\n")))
}
