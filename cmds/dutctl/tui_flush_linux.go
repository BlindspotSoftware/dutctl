// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package main

import "golang.org/x/sys/unix"

// discardInput drops the keys still queued on the terminal fd, typed during a
// run and taken by no console, so that they cannot act on the prompt.
func discardInput(fd int) {
	_ = unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)
}
