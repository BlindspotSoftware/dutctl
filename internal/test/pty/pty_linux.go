// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pty provides pseudo-terminals for tests that need a terminal, such
// as tests of a raw console.
package pty

import (
	"bytes"
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// readTimeout bounds ReadUntil.
	readTimeout = 5 * time.Second
	// readChunk is the size of one read in ReadUntil.
	readChunk = 256
)

// Open opens a pseudo-terminal pair and returns its master and its slave end.
// The test is skipped where none is available; both ends close with the test.
func Open(t *testing.T) (*os.File, *os.File) {
	t.Helper()

	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal available: %v", err)
	}

	t.Cleanup(func() { _ = master.Close() })

	masterFd := int(master.Fd())

	unlock := 0

	err = unix.IoctlSetPointerInt(masterFd, unix.TIOCSPTLCK, unlock)
	if err != nil {
		t.Skipf("cannot unlock the pseudo-terminal: %v", err)
	}

	num, err := unix.IoctlGetInt(masterFd, unix.TIOCGPTN)
	if err != nil {
		t.Skipf("cannot name the pseudo-terminal: %v", err)
	}

	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", num), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("cannot open the pseudo-terminal: %v", err)
	}

	t.Cleanup(func() { _ = slave.Close() })

	return master, slave
}

// Termios returns the terminal attributes of f.
func Termios(t *testing.T, f *os.File) *unix.Termios {
	t.Helper()

	tio, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatalf("TCGETS: %v", err)
	}

	return tio
}

// ReadUntil reads from file until want arrived and returns what arrived. The
// files of a pseudo-terminal are in blocking mode once their Fd was taken, so
// a deadline would not bound the read: it runs on a goroutine, and the test
// fails if want does not arrive in time.
func ReadUntil(t *testing.T, file *os.File, want string) string {
	t.Helper()

	got := make(chan string, 1)

	go func() {
		var out []byte

		buf := make([]byte, readChunk)

		for !bytes.Contains(out, []byte(want)) {
			count, err := file.Read(buf)
			out = append(out, buf[:count]...)

			if err != nil {
				break
			}
		}

		got <- string(out)
	}()

	select {
	case out := <-got:
		return out
	case <-time.After(readTimeout):
		t.Fatalf("nothing containing %q arrived within %s", want, readTimeout)

		return ""
	}
}
