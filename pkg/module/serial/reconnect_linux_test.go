// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func openReconnectPTY(t *testing.T) (*os.File, string) {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(fd), "pty-master")
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	return master, fmt.Sprintf("/dev/pts/%d", number)
}

func TestReconnectRealPTYReplacement(t *testing.T) {
	first, firstPath := openReconnectPTY(t)
	link := filepath.Join(t.TempDir(), "stable-serial-id")
	if err := os.Symlink(firstPath, link); err != nil {
		t.Fatal(err)
	}
	run := newInteractiveRun()
	run.serial.Port = link
	run.serial.open = nil // Exercise serial.Open, real hangup errors and the configured read timeout.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); _ = run.input.Close() })
	done := run.start(ctx, "-reconnect", "-t", "5s")
	waitReconnect(t, func() bool { return strings.Contains(run.stderr.String(), "Connected to") })
	if _, err := first.Write([]byte("before\r\n")); err != nil {
		t.Fatal(err)
	}
	waitReconnect(t, func() bool { return strings.Contains(run.stdout.String(), "before") })
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	waitReconnect(t, func() bool { return strings.Contains(run.stderr.String(), "Serial device disconnected:") })
	second, secondPath := openReconnectPTY(t)
	if err := os.Symlink(secondPath, link); err != nil {
		t.Fatal(err)
	}
	waitReconnect(t, func() bool { return strings.Contains(run.stderr.String(), "reconnected:") })
	settings, err := unix.IoctlGetTermios(int(second.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Cflag&unix.CBAUD != unix.B115200 {
		t.Errorf("replacement baud=%#x, want B115200", settings.Cflag&unix.CBAUD)
	}
	if _, err := second.Write([]byte("after\r\n")); err != nil {
		t.Fatal(err)
	}
	waitReconnect(t, func() bool { return strings.Contains(run.stdout.String(), "after") })
	cancel()
	if err := awaitReconnectRun(t, done); err != nil {
		t.Fatal(err)
	}
	if got := run.stdout.String(); got != "before\r\nafter\r\n" {
		t.Errorf("raw output across real hangup=%q", got)
	}
	if run.stdin.inRead.Load() != 0 {
		t.Error("input goroutine left running after real port teardown")
	}
}
