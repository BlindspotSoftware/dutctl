// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package integration

// The tests of the serial module against a serial line that socat makes of
// two connected pseudo-terminals: the agent opens one end as its port, and
// the test plays the device at the other. They pin the byte path through the
// real client, agent and port driver: nothing added, nothing stripped, in
// either direction.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/BlindspotSoftware/dutctl/internal/test/pty"
)

// serialLine is a serial line between the agent's port and a device the test
// plays.
type serialLine struct {
	// port is the agent's end, the path the serial module opens.
	port string
	// device is the device's end.
	device *os.File
	// received collects what reaches the device.
	received *stream
	// socat is the process that connects the two ends.
	socat *process
}

// startSerialLine starts socat with a serial line of two pseudo-terminals,
// both raw and without echo. The test is skipped without socat.
func startSerialLine(t *testing.T) *serialLine {
	t.Helper()
	build(t)

	socat, err := exec.LookPath("socat")
	if err != nil {
		t.Skip("socat is not installed")
	}

	dir := t.TempDir()
	line := &serialLine{port: filepath.Join(dir, "port"), received: newStream()}
	device := filepath.Join(dir, "device")

	line.socat = start(t, exec.Command(socat, "-d", "-d",
		"pty,rawer,echo=0,link="+device, "pty,rawer,echo=0,link="+line.port))
	line.socat.stderr.waitFor(t, "starting data transfer loop")

	line.device, err = os.OpenFile(device, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = line.device.Close() })

	go func() { _, _ = io.Copy(line.received, line.device) }()

	return line
}

// send writes s to the line, as the device.
func (l *serialLine) send(t *testing.T, s string) {
	t.Helper()

	_, err := l.device.WriteString(s)
	if err != nil {
		t.Fatalf("sending as the device: %v", err)
	}
}

// serialAgent starts an agent whose device dut has an interactive console,
// a boot command and a scripted command on the line's port.
func serialAgent(t *testing.T, line *serialLine) *agent {
	t.Helper()

	return startAgentYAML(t, fmt.Sprintf(`
devices:
  dut:
    cmds:
      console:
        uses:
          - module: serial
            args: ["-i"]
            with:
              port: %[1]s
      boot:
        uses:
          - module: wrap-print
            args: [power-cycle]
          - module: serial
            args: ["-i"]
            with:
              port: %[1]s
      script:
        uses:
          - module: serial
            passthrough: true
            with:
              port: %[1]s
`, line.port))
}

// The interactive console from a pipe: the device gets the bytes as typed,
// CR included; the user's stdout gets the device's bytes as sent, escape
// sequences included, and nothing else; the end of the input ends the run.
func TestSerialConsoleFromAPipe(t *testing.T) {
	t.Parallel()

	line := startSerialLine(t)
	agt := serialAgent(t, line)

	console := agt.start(t, "dut", "console")
	console.stderr.waitFor(t, "--- Connected to "+line.port+" at 115200 baud ---")

	console.write(t, "ls\r")
	line.received.waitFor(t, "ls\r")

	reply := "BOOT\r\n\x1b[1mok\x1b[0m\r\n"
	line.send(t, reply)
	console.stdout.waitFor(t, reply)
	console.closeInput(t)

	res := console.wait(t)
	mustSucceed(t, res)

	if res.stdout != reply {
		t.Errorf("stdout %q, want exactly the device's bytes %q", res.stdout, reply)
	}

	if got := line.received.String(); got != "ls\r" {
		t.Errorf("the device received %q, want exactly %q", got, "ls\r")
	}

	inOrder(t, res.stderr, "Connected to", "Connection closed")
}

// The interactive console on a terminal: raw mode, keys as typed, the
// device's bytes as sent, the escape sequence for local echo and for the end
// of the session, and the terminal restored afterwards.
func TestSerialConsoleOnATerminal(t *testing.T) {
	t.Parallel()

	line := startSerialLine(t)
	agt := serialAgent(t, line)

	term := agt.startOnTerminal(t, "-no-color", "dut", "console")
	before := pty.Termios(t, term.slave)

	term.stderr.waitFor(t, "Connected to")

	if raw := pty.Termios(t, term.slave); raw.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG) != 0 || raw.Iflag&unix.ICRNL != 0 {
		t.Errorf("the console's terminal is not raw: lflag %#x iflag %#x", raw.Lflag, raw.Iflag)
	}

	// Ctrl-C and Enter reach the device as bytes.
	term.write(t, "\x03a\r")
	line.received.waitFor(t, "\x03a\r")

	line.send(t, "\x1b[31mred\x1b[0m")
	term.screen.waitFor(t, "\x1b[31mred\x1b[0m")

	// Ctrl-A e switches the local echo on: the device does not echo, the
	// terminal shows the key anyway.
	term.write(t, "\x01e")
	term.screen.waitFor(t, "local echo on")
	term.write(t, "z")
	line.received.waitFor(t, "\x03a\rz")
	term.screen.waitMatch(t, `"z" after the echo's notice`, func(got string) bool {
		return ordered(got, "local echo on", "z")
	})

	// Ctrl-A x ends the session.
	term.write(t, "\x01x")

	res := term.wait(t)
	mustSucceed(t, res)
	inOrder(t, res.stderr, "Connection closed")

	if got := line.received.String(); got != "\x03a\rz" {
		t.Errorf("the device received %q, want the keys without the escape sequences", got)
	}

	if after := pty.Termios(t, term.slave); after.Lflag != before.Lflag || after.Iflag != before.Iflag || after.Oflag != before.Oflag {
		t.Errorf("the terminal was not restored: lflag %#x/%#x iflag %#x/%#x oflag %#x/%#x",
			after.Lflag, before.Lflag, after.Iflag, before.Iflag, after.Oflag, before.Oflag)
	}
}

// A step before the console runs before it, and its output comes first: how
// a command that resets a device before its console is shaped.
func TestSerialStepBeforeTheConsole(t *testing.T) {
	t.Parallel()

	line := startSerialLine(t)
	agt := serialAgent(t, line)

	boot := agt.start(t, "dut", "boot")
	boot.stderr.waitFor(t, "Connected to")

	boot.write(t, "x")
	line.received.waitFor(t, "x")
	line.send(t, "U-Boot\r\n")
	boot.stdout.waitFor(t, "U-Boot\r\n")
	boot.closeInput(t)

	res := boot.wait(t)
	mustSucceed(t, res)
	inOrder(t, res.stdout, "power-cycle\n", "U-Boot\r\n")
}

// A send/expect script runs against the device.
func TestSerialScript(t *testing.T) {
	t.Parallel()

	line := startSerialLine(t)
	agt := serialAgent(t, line)

	script := agt.start(t, "dut", "script", "-t", "10s", "--", "send", "ping", "expect", "pong")
	line.received.waitFor(t, "ping\r")
	line.send(t, "pong\r\n")
	mustSucceed(t, script.wait(t))

	mustFail(t, agt.dutctl(t, "dut", "script", "-t", "200ms", "--", "expect", "never"), 1,
		`step 1 (expect "never"): timeout of 200ms reached without match`)
}

// A port that goes away ends the session with an error.
func TestSerialPortGone(t *testing.T) {
	t.Parallel()

	line := startSerialLine(t)
	agt := serialAgent(t, line)

	console := agt.start(t, "dut", "console")
	console.stderr.waitFor(t, "Connected to")

	_ = line.socat.cmd.Process.Kill()

	mustFail(t, console.wait(t), 1, "interactive")
}

// A user's second command on a port in use fails when it opens the port:
// the serial library opens a port for exclusive use.
func TestSerialPortInUse(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root opens a port that is in exclusive use")
	}

	line := startSerialLine(t)
	agt := serialAgent(t, line)

	console := agt.start(t, "dut", "console")
	console.stderr.waitFor(t, "Connected to")

	mustFail(t, agt.dutctl(t, "dut", "script", "--", "send", "x"), 1, "failed to open serial port "+line.port)

	console.closeInput(t)
	mustSucceed(t, console.wait(t))

	if strings.Contains(line.received.String(), "x") {
		t.Errorf("the refused command reached the device: %q", line.received)
	}
}
