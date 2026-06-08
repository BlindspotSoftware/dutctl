// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin

package main

import "golang.org/x/sys/unix"

// Terminal get/set ioctl request numbers for macOS (BSD-derived).
const (
	tcGetReq = unix.TIOCGETA
	tcSetReq = unix.TIOCSETA
)
