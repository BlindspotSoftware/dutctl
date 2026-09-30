// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux || darwin

package main

import "golang.org/x/sys/unix"

// setRawInput puts the terminal into raw input mode so the interactive serial
// session behaves like a direct console:
//
//   - ECHO/ICANON off: keystrokes are delivered immediately, character at a
//     time, and not echoed locally (the DUT echoes them back).
//   - ISIG off: control characters such as Ctrl-C, Ctrl-Z and Ctrl-\ are NOT
//     turned into local signals; they are forwarded to the DUT as raw bytes.
//   - IXON off: Ctrl-S/Ctrl-Q flow control is forwarded to the DUT instead of
//     being swallowed by the local terminal.
//   - IEXTEN off: Ctrl-V and friends are forwarded literally.
//   - ICRNL off: a typed CR is sent as CR, not translated to NL.
//
// dutctl is exited with the client-side escape sequence (Ctrl-A x), not with a
// terminal signal — see filterEscape in rpc.go.
//
// It returns a restore function, or nil if the fd is not a terminal (in which
// case input stays line-buffered, which is the correct fallback for pipes).
//
// The ioctl request numbers differ per OS (tcGetReq/tcSetReq are defined in the
// platform-specific files); the termios flags themselves are shared across
// unix platforms.
func setRawInput(fileDescriptor int) func() {
	termios, err := unix.IoctlGetTermios(fileDescriptor, tcGetReq)
	if err != nil {
		return nil
	}

	old := *termios

	termios.Iflag &^= unix.ICRNL | unix.IXON
	termios.Lflag &^= unix.ECHO | unix.ICANON | unix.ISIG | unix.IEXTEN
	termios.Cc[unix.VMIN] = 1
	termios.Cc[unix.VTIME] = 0

	err = unix.IoctlSetTermios(fileDescriptor, tcSetReq, termios)
	if err != nil {
		return nil
	}

	return func() {
		_ = unix.IoctlSetTermios(fileDescriptor, tcSetReq, &old)
	}
}
