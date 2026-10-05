// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"

	"golang.org/x/term"
)

// ttyFd returns the file descriptor behind stream if stream is a terminal: an
// *os.File that termios accepts, the POSIX terminal interface (tcgetattr and
// tcsetattr) behind raw mode, which x/term wraps. That excludes pipes, regular
// files and other character devices such as /dev/null.
func ttyFd(stream any) (int, bool) {
	file, ok := stream.(*os.File)
	if !ok {
		return 0, false
	}

	fd := int(file.Fd())

	return fd, term.IsTerminal(fd)
}

// isTTY reports whether stream is a terminal, by the probe of ttyFd. It gates
// colour as well as what the console writes to the terminal while raw.
func isTTY(stream any) bool {
	_, ok := ttyFd(stream)

	return ok
}
