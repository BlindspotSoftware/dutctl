// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// openPty opens a pseudo-terminal pair and returns both ends; the test is
// skipped where none is available.
func openPty(t *testing.T) (*os.File, *os.File) {
	t.Helper()

	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal available: %v", err)
	}

	t.Cleanup(func() { _ = master.Close() })

	fd := int(master.Fd())

	unlock := 0
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, unlock); err != nil {
		t.Skipf("cannot unlock the pseudo-terminal: %v", err)
	}

	num, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
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

func termios(t *testing.T, f *os.File) *unix.Termios {
	t.Helper()

	tio, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatalf("TCGETS: %v", err)
	}

	return tio
}

// readUntil reads from f on a goroutine until want appears or the timeout
// passes, and returns what arrived. The console's Fd call put the files into
// blocking mode, so a deadline would not bound the read; a goroutine does.
func readUntil(t *testing.T, f *os.File, want string) string {
	t.Helper()

	got := make(chan string, 1)

	go func() {
		var out []byte

		buf := make([]byte, 256)

		for !bytes.Contains(out, []byte(want)) {
			n, err := f.Read(buf)
			out = append(out, buf[:n]...)

			if err != nil {
				break
			}
		}

		got <- string(out)
	}()

	select {
	case out := <-got:
		return out
	case <-time.After(2 * time.Second):
		t.Fatalf("nothing containing %q arrived within the timeout", want)

		return ""
	}
}

// On a terminal a RAW console switches to raw mode, so keys arrive at once
// and as they are, Ctrl-C included, console output is written as it is, the
// client's own text still starts its lines at column 0, and closing restores
// the terminal; a LINE console leaves the terminal alone. stdout is the
// terminal here, so what the client writes is read back from the master.
func TestConsoleRawModeOnTerminal(t *testing.T) {
	master, slave := openPty(t)

	before := termios(t, slave)
	if before.Lflag&unix.ICANON == 0 || before.Lflag&unix.ECHO == 0 {
		t.Fatal("the fresh pseudo-terminal is not in canonical mode with echo; the test cannot tell raw from cooked")
	}

	var stderr bytes.Buffer

	con := newConsole(slave, slave, &stderr, true)
	if !con.tty || !con.outTTY {
		t.Fatal("the pseudo-terminal was not recognised as a terminal")
	}

	con.opened(1, pb.ConsoleMode_CONSOLE_MODE_LINE)

	if con.rawActive() {
		t.Error("a LINE console switched the terminal to raw mode")
	}

	con.closed()
	con.opened(2, pb.ConsoleMode_CONSOLE_MODE_RAW)

	if !con.rawActive() {
		t.Fatal("a RAW console did not switch the terminal to raw mode")
	}

	raw := termios(t, slave)
	if raw.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG|unix.IEXTEN) != 0 {
		t.Errorf("raw mode left ICANON/ECHO/ISIG/IEXTEN on: lflag %#x", raw.Lflag)
	}

	if raw.Iflag&(unix.ICRNL|unix.IXON) != 0 {
		t.Errorf("raw mode left ICRNL/IXON on: iflag %#x", raw.Iflag)
	}

	if !bytes.Contains(stderr.Bytes(), []byte("Ctrl-A x")) {
		t.Errorf("no hint about the escape sequence on stderr: %q", stderr.String())
	}

	// Keys typed on the terminal arrive at once and unchanged: a Ctrl-C and a
	// CR, which a cooked terminal would turn into a signal and a newline.
	if _, err := master.Write([]byte("\x03a\r")); err != nil {
		t.Fatal(err)
	}

	if got := readUntil(t, slave, "\r"); got != "\x03a\r" {
		t.Errorf("keys arrived as %q, want \"\\x03a\\r\"", got)
	}

	// Console output goes to the terminal as it is: no output processing
	// while raw, so the bare escape sequence comes back from the master.
	if !con.output([]byte("out\x1b[31m"), false) {
		t.Error("raw console output was not written directly")
	}

	if got := readUntil(t, master, "\x1b[31m"); got != "out\x1b[31m" {
		t.Errorf("console output arrived as %q, want the raw bytes", got)
	}

	// The client's own text starts its lines at column 0 while raw: the
	// kernel adds no CR with output processing off, the wrapper does.
	fmt.Fprint(con.outText, "line\n")

	if got := readUntil(t, master, "\n"); got != "line\r\n" {
		t.Errorf("client text while raw arrived as %q, want \"line\\r\\n\"", got)
	}

	con.closed()

	if con.rawActive() {
		t.Error("raw mode still on after close")
	}

	after := termios(t, slave)
	if after.Lflag != before.Lflag || after.Iflag != before.Iflag || after.Oflag != before.Oflag {
		t.Errorf("terminal not restored: lflag %#x/%#x iflag %#x/%#x oflag %#x/%#x",
			after.Lflag, before.Lflag, after.Iflag, before.Iflag, after.Oflag, before.Oflag)
	}

	// The close moved the cursor to column 0 on the terminal.
	if got := readUntil(t, master, "\n"); !bytes.HasSuffix([]byte(got), []byte("\r\n")) {
		t.Errorf("after the close the terminal received %q, want a line end", got)
	}
}

// With stdout redirected, the terminal housekeeping stays off it: a capture
// holds the module's bytes and nothing else, and the client's own text keeps
// its plain line ends.
func TestConsoleRedirectedStdoutStaysClean(t *testing.T) {
	_, slave := openPty(t)

	var stdout, stderr bytes.Buffer

	con := newConsole(slave, &stdout, &stderr, true)
	con.opened(1, pb.ConsoleMode_CONSOLE_MODE_RAW)

	if !con.rawActive() {
		t.Fatal("a RAW console on a terminal stdin did not switch to raw mode")
	}

	con.output([]byte("dut"), false)
	fmt.Fprint(con.outText, "print\n")
	con.closed()

	if got := stdout.String(); got != "dutprint\n" {
		t.Errorf("redirected stdout = %q, want the module's bytes and plain client text only", got)
	}

	if !bytes.Contains(stderr.Bytes(), []byte("Ctrl-A x")) {
		t.Errorf("no hint on stderr: %q", stderr.String())
	}
}

// A RAW console under a structured output format leaves the terminal cooked.
func TestConsoleRawRefusedForStructuredOutput(t *testing.T) {
	_, slave := openPty(t)

	con := newConsole(slave, &bytes.Buffer{}, &bytes.Buffer{}, false)
	con.opened(1, pb.ConsoleMode_CONSOLE_MODE_RAW)

	if con.rawActive() {
		t.Error("raw mode switched on although the output format is structured")
	}

	con.closed()
}
