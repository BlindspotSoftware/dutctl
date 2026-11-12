// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package integration

// The tests of the module.Session contract, end to end through the demo
// modules: what a module does reaches the user, and what the user does
// reaches the module, as pkg/module documents it.

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/BlindspotSoftware/dutctl/internal/test/pty"
)

// The Print family's output, and a console's, reach the user in the order
// the modules of a command produce it.
func TestSessionOutputKeepsItsOrder(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	res := agt.dutctl(t, "demo-board", "reboot")
	mustSucceed(t, res)

	inOrder(t, res.stdout,
		"The power line is already off.\n",
		"Switching it on again in 500ms.\n",
		"The power line is now on.\n",
		"Booting factory.bin",
		"Welcome to the demo board.")

	inOrder(t, res.stderr, "Connected to the demo board, which is on", "Connection closed")
}

// Input the user sends before a console opens waits for it: dutctl reads
// none while no console is open, so the console of a command's later module
// reads what was piped in at the start.
func TestSessionInputBeforeAConsoleReachesIt(t *testing.T) {
	t.Parallel()

	agt := startAgentYAML(t, `
devices:
  board:
    cmds:
      boot:
        uses:
          - module: demo-switch
            args:
              - cycle
            with:
              line: power
              cycle: 200ms
          - module: demo-console
`)

	res := agt.dutctlInput(t, "exit\r", "board", "boot")
	mustSucceed(t, res)
	inOrder(t, res.stdout, "The power line is now on.\n", "Booting factory.bin", "demo# exit\r\nlogout\r\n")
}

// A raw console served from a pipe: the input's end reaches the module, which
// still shows the board's last output and its closing marker.
func TestSessionConsoleOutputGoesOnAfterTheInputEnded(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))
	mustSucceed(t, agt.dutctl(t, "demo-board", "power", "on"))

	res := agt.dutctlInput(t, "version\r", "demo-board", "console")
	mustSucceed(t, res)

	// What the board sent since it booted comes first, then its reply.
	inOrder(t, res.stdout, "Booting factory.bin", "demo# version\r\nfactory.bin, 24 bytes, sha256 ", "\r\ndemo# ")

	if strings.Contains(res.stdout, "Connected") || !strings.HasSuffix(res.stderr, "--- Connection closed ---\r\n") {
		t.Errorf("the markers are not on stderr alone:\nstdout %q\nstderr %q", res.stdout, res.stderr)
	}

	agt.waitRecord(t, "console ended: the input ended", "module", "demo-console")
}

// A module may end its console while the user's input is open: it closes
// Stdin, which ends the read that its own goroutine blocks in.
func TestSessionModuleEndsAConsoleWhoseInputIsOpen(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))
	mustSucceed(t, agt.dutctl(t, "demo-board", "power", "on"))

	console := agt.start(t, "demo-board", "console")
	console.stdout.waitFor(t, "demo# ")
	console.write(t, "exit\r")

	res := console.wait(t)
	mustSucceed(t, res)

	if !strings.HasSuffix(res.stdout, "exit\r\nlogout\r\n") {
		t.Errorf("stdout %q does not end in the logout", res.stdout)
	}

	agt.waitRecord(t, "console ended: the board's shell logged out", "module", "demo-console")
}

// A client that disconnects tears the session down: the module sees its
// console end, returns, and the device is free at once.
func TestSessionTeardownWhenTheClientIsGone(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	console := agt.start(t, "demo-board", "console")
	console.stderr.waitFor(t, "Connected to the demo board")
	console.signal(t, syscall.SIGKILL)
	console.wait(t)

	agt.waitRecord(t, "console ended: the session was torn down", "module", "demo-console")
	agt.waitRecord(t, "request cancelled", "rpc", "Run")

	mustSucceed(t, agt.dutctl(t, "demo-board", "console"))
}

// Ctrl-C cancels the command's context, the module returns at once, and the
// device is free for the next command.
func TestSessionInterruptCancelsTheModule(t *testing.T) {
	t.Parallel()

	agt := startAgentYAML(t, `
devices:
  board:
    cmds:
      power:
        uses:
          - module: demo-switch
            passthrough: true
            with:
              line: power
              cycle: 1h
`)

	cycle := agt.start(t, "board", "power", "cycle")
	cycle.stdout.waitFor(t, "Switching it on again in 1h0m0s.")
	cycle.signal(t, os.Interrupt)

	res := cycle.wait(t)
	mustFail(t, res, 130, "interrupted")

	agt.waitRecord(t, "module stopped after the command was cancelled", "module", "demo-switch")

	res = agt.dutctl(t, "board", "power")
	mustSucceed(t, res)

	if res.stdout != "The power line is off.\n" {
		t.Errorf("after the aborted cycle, power printed %q, want the board off", res.stdout)
	}
}

// Input for a command that opens no console is never read.
func TestSessionInputWithoutAConsoleIsIgnored(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	res := agt.dutctlInput(t, "on\n", "demo-board", "power", "status")
	mustSucceed(t, res)

	if res.stdout != "The power line is off.\n" {
		t.Errorf("status printed %q", res.stdout)
	}
}

// A line console served from a pipe: the module assembles the line from
// what it reads, also a last line without its newline.
func TestSessionLineConsoleFromAPipe(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	for _, input := range []string{"Ada\n", "Ada"} {
		res := agt.dutctlInput(t, input, "agent", "prompt")
		mustSucceed(t, res)

		if want := "What is your name? Hello, Ada!\n"; res.stdout != want {
			t.Errorf("for input %q, stdout %q, want %q", input, res.stdout, want)
		}
	}
}

// A raw console served from a pipe: the input arrives as it is read, and its
// end ends the console; Print goes on while the console is open.
func TestSessionRawConsoleFromAPipe(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	res := agt.dutctlInput(t, "ab\n", "agent", "keys")
	mustSucceed(t, res)

	want := "Raw console: every key arrives at once, as it is; a lone Ctrl-D ends it.\n" + "61 62 0a\r\n"
	if res.stdout != want {
		t.Errorf("stdout %q, want %q", res.stdout, want)
	}
}

// Under a structured output format, Print and console output are records of
// their own, in the order the module made them.
func TestSessionConsoleOutputAsJSON(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	res := agt.dutctlInput(t, "ab\n", "-f", "json", "agent", "keys")
	mustSucceed(t, res)

	var data []string

	dec := json.NewDecoder(strings.NewReader(res.stdout))

	for {
		var rec struct {
			ContentType string `json:"contentType"`
			Data        string `json:"data"`
		}

		err := dec.Decode(&rec)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("stdout is no stream of JSON records: %v\n%s", err, res.stdout)
		}

		if rec.ContentType != "module-output" {
			t.Errorf("record of type %q, want module-output", rec.ContentType)
		}

		data = append(data, rec.Data)
	}

	if len(data) != 2 || !strings.HasPrefix(data[0], "Raw console") || data[1] != "61 62 0a\r\n" {
		t.Errorf("records %q, want the console's instructions, then the bytes", data)
	}
}

// On a terminal, a line console leaves the terminal as it is: it edits the
// line and echoes it, and Enter sends it.
func TestSessionLineConsoleOnATerminal(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))
	term := agt.startOnTerminal(t, "-no-color", "agent", "prompt")

	term.screen.waitFor(t, "What is your name? ")

	if pty.Termios(t, term.slave).Lflag&unix.ICANON == 0 {
		t.Error("a line console switched the terminal out of canonical mode")
	}

	term.write(t, "Ad\x7fda\r")
	term.screen.waitFor(t, "Hello, Ada!")

	mustSucceed(t, term.wait(t))
}

// On a terminal, a raw console switches it to raw mode, where every key
// arrives at once and unchanged; the end of the run restores it.
func TestSessionRawConsoleOnATerminal(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))
	term := agt.startOnTerminal(t, "-no-color", "agent", "keys")

	before := pty.Termios(t, term.slave)

	term.screen.waitFor(t, "Raw console")
	term.stderr.waitFor(t, "raw console: Ctrl-A x ends it")

	if raw := pty.Termios(t, term.slave); raw.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG) != 0 {
		t.Errorf("a raw console left ICANON, ECHO or ISIG on: lflag %#x", raw.Lflag)
	}

	// An arrow key arrives as its escape sequence, Ctrl-C as a byte.
	term.write(t, "\x1b[A")
	term.screen.waitFor(t, "1b 5b 41")
	term.write(t, "\x03")
	term.screen.waitFor(t, "\r\n03\r\n")
	term.write(t, "\x04")

	mustSucceed(t, term.wait(t))

	if after := pty.Termios(t, term.slave); after.Lflag != before.Lflag || after.Iflag != before.Iflag || after.Oflag != before.Oflag {
		t.Errorf("the terminal was not restored: lflag %#x/%#x iflag %#x/%#x oflag %#x/%#x",
			after.Lflag, before.Lflag, after.Iflag, before.Iflag, after.Oflag, before.Oflag)
	}
}

// Ctrl-C on a line console interrupts dutctl, which ends the run.
func TestSessionCtrlCInALineConsoleInterrupts(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))
	term := agt.startOnTerminal(t, "-no-color", "agent", "prompt")

	term.screen.waitFor(t, "What is your name? ")
	term.write(t, "\x03")

	mustFail(t, term.wait(t), 130, "interrupted")
	agt.waitRecord(t, "request cancelled", "rpc", "Run")
}

// A file goes from the user's machine to the module and back.
func TestSessionFileTransfer(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	err := os.WriteFile(filepath.Join(agt.dir, "fw.bin"), []byte("firmware 2.0\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	res := agt.dutctl(t, "agent", "upload", "fw.bin")
	mustSucceed(t, res)
	inOrder(t, res.stdout, `sent "fw.bin"`, "Received fw.bin: 13 bytes, sha256 ")

	res = agt.dutctl(t, "agent", "download", "note.txt")
	mustSucceed(t, res)
	inOrder(t, res.stdout, `received "note.txt"`, "Sent note.txt: 48 bytes.")

	got, err := os.ReadFile(filepath.Join(agt.dir, "note.txt"))
	if err != nil || string(got) != "This file was sent by the wrap-download module.\n" {
		t.Errorf("note.txt holds %q (%v)", got, err)
	}
}

// A file the user cannot provide ends the run with an error on the client,
// and the module's request fails.
func TestSessionFileTheUserCannotProvide(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	mustFail(t, agt.dutctl(t, "demo-board", "flash", "write", "missing.bin"), 1, `"missing.bin"`)
	agt.waitRecord(t, "request cancelled", "rpc", "Run")

	// The flash still holds what it held, and the device is free.
	res := agt.dutctl(t, "demo-board", "flash", "read", "copy.bin")
	mustSucceed(t, res)

	if !strings.Contains(res.stdout, "Read factory.bin") {
		t.Errorf("after the failed write, read printed %q", res.stdout)
	}
}

// A module's error reaches the user; a module's panic does too, and the agent
// goes on serving.
func TestSessionModuleErrorsAndPanics(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	mustFail(t, agt.dutctl(t, "agent", "fail"), 1, "module failed: the device did not answer")
	mustFail(t, agt.dutctl(t, "agent", "crash"), 1, "module panicked: the wrap-fault module failed as asked")

	mustSucceed(t, agt.dutctl(t, "demo-board", "power", "on"))
	mustFail(t, agt.dutctl(t, "demo-board", "flash", "write", "fw.bin"), 1, "power it off before writing its flash")
}
