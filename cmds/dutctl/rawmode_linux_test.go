// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// openPty opens a pseudo-terminal pair and returns its terminal (slave) end,
// which behaves like the stdin of an interactive dutctl. The test is skipped
// where no pseudo-terminal can be allocated.
func openPty(t *testing.T) *os.File {
	t.Helper()

	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal available: %v", err)
	}

	t.Cleanup(func() { _ = master.Close() })

	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlock pty: %v", err)
	}

	num, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("pty number: %v", err)
	}

	term, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", num), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pty terminal: %v", err)
	}

	t.Cleanup(func() { _ = term.Close() })

	return term
}

func termios(t *testing.T, f *os.File) *unix.Termios {
	t.Helper()

	tio, err := unix.IoctlGetTermios(int(f.Fd()), tcGetReq)
	if err != nil {
		t.Fatalf("get termios: %v", err)
	}

	return tio
}

// checkRaw fails the test unless tio is in the raw input mode setRawInput sets.
func checkRaw(t *testing.T, tio *unix.Termios) {
	t.Helper()

	if tio.Lflag&(unix.ECHO|unix.ICANON|unix.ISIG|unix.IEXTEN) != 0 {
		t.Errorf("lflag = %#x, want ECHO, ICANON, ISIG and IEXTEN off", tio.Lflag)
	}

	if tio.Iflag&(unix.ICRNL|unix.IXON) != 0 {
		t.Errorf("iflag = %#x, want ICRNL and IXON off", tio.Iflag)
	}

	if tio.Cc[unix.VMIN] != 1 || tio.Cc[unix.VTIME] != 0 {
		t.Errorf("VMIN/VTIME = %d/%d, want 1/0", tio.Cc[unix.VMIN], tio.Cc[unix.VTIME])
	}
}

func TestSetRawInputAndRestore(t *testing.T) {
	term := openPty(t)
	before := *termios(t, term)

	restore := setRawInput(int(term.Fd()))
	if restore == nil {
		t.Fatal("setRawInput returned nil for a terminal")
	}

	checkRaw(t, termios(t, term))

	restore()

	if after := *termios(t, term); after != before {
		t.Errorf("termios after restore = %+v, want the original %+v", after, before)
	}
}

func TestSetRawInputLeavesNonTerminalAlone(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	defer r.Close()
	defer w.Close()

	if restore := setRawInput(int(r.Fd())); restore != nil {
		t.Error("setRawInput returned a restore function for a pipe, want nil")
	}
}

func TestRawConsoleArmsAndDisarmsOnTerminal(t *testing.T) {
	term := openPty(t)
	before := *termios(t, term)

	var banner bytes.Buffer

	console := newRawConsole(term, &banner)

	console.arm()
	console.arm() // one arm per console message; only the first acts

	if !console.isActive() {
		t.Fatal("isActive() = false on a terminal, want true")
	}

	checkRaw(t, termios(t, term))

	if n := strings.Count(banner.String(), "press Ctrl-A then x to quit"); n != 1 {
		t.Errorf("quit hint printed %d times, want once: %q", n, banner.String())
	}

	console.disarm()
	console.disarm() // deferred by runRPC on every exit path; must be safe twice

	if after := *termios(t, term); after != before {
		t.Errorf("termios after disarm = %+v, want the original %+v", after, before)
	}
}
