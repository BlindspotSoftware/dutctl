// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin

package main

import "golang.org/x/sys/unix"

// flushRead selects the input queue for TIOCFLUSH: FREAD from <sys/fcntl.h>,
// which x/sys does not export.
const flushRead = 1

// discardInput drops the keys still queued on the terminal fd, typed during a
// run and taken by no console, so that they cannot act on the prompt.
func discardInput(fd int) {
	_ = unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, flushRead)
}
