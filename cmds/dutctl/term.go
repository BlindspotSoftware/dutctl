// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"io"
	"os"

	"golang.org/x/term"
)

// ttyFd returns r's file descriptor if r is a terminal: an *os.File the
// termios calls accept, which excludes pipes, regular files and /dev/null.
func ttyFd(r io.Reader) (int, bool) {
	file, ok := r.(*os.File)
	if !ok {
		return 0, false
	}

	fd := int(file.Fd())

	return fd, term.IsTerminal(fd)
}

// isTerminal reports whether w is connected to an interactive terminal (TTY).
//
// Caveat of the stdlib: character devices such as /dev/null also
// report true. That is harmless here (nothing reads color written to
// /dev/null), while pipes and regular files — the cases that matter — correctly
// report false.
func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}

	info, err := file.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

// isTTY reports whether w is a terminal, by the termios probe: unlike
// isTerminal, it rules out other character devices such as /dev/null, which
// matters for what the console writes to the terminal while raw.
func isTTY(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}

	return term.IsTerminal(int(file.Fd()))
}
