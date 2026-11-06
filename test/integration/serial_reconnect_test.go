// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlindspotSoftware/dutctl/internal/test/pty"
)

func TestSerialReconnectEmptyExpectRequiresConnection(t *testing.T) {
	t.Parallel()

	agt := serialAgent(t, filepath.Join(t.TempDir(), "absent-serial-by-id"))
	res := agt.dutctl(t, "dut", "script", "-reconnect", "-t", "100ms", "expect", ".*")
	mustFail(t, res, 1, `step 1 (expect ".*"): timeout of 100ms reached without match`)
	if strings.Contains(res.stdout, "Script completed") {
		t.Errorf("script succeeded without a connection: %q", res.stdout)
	}
}

func TestSerialReconnectEmptyExpectConnectsBeforeMatching(t *testing.T) {
	t.Parallel()

	line := startSerialLine(t)
	agt := serialAgent(t, line.port)
	res := agt.dutctl(t, "dut", "script", "-reconnect", "expect", ".*")
	mustSucceed(t, res)
	inOrder(t, res.stdout, "Connected to "+line.port, `matched ".*"`, "Script completed")
}

func TestSerialConsoleReconnect(t *testing.T) {
	t.Parallel()

	first := startSerialLine(t)
	stable := filepath.Join(t.TempDir(), "serial-by-id")
	if err := os.Symlink(first.port, stable); err != nil {
		t.Fatal(err)
	}
	agt := serialAgent(t, stable)
	console := agt.startOnTerminal(t, "-no-color", "dut", "script", "-reconnect", "-i")
	before := console.original
	console.stderr.waitFor(t, "Connected to "+stable)

	console.write(t, "before\r")
	first.received.waitFor(t, "before\r")
	firstOutput := "\x1b[31mbefore\x1b[0m\r\n"
	first.send(t, firstOutput)
	console.screen.waitFor(t, firstOutput)

	if err := os.Remove(stable); err != nil {
		t.Fatal(err)
	}
	if err := first.socat.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	first.socat.wait(t)
	console.stderr.waitFor(t, "Waiting for serial device to reconnect: "+stable)
	console.write(t, "must-not-replay\r")
	console.stderr.waitFor(t, "Input while disconnected is discarded")

	second := startSerialLine(t)
	if err := os.Symlink(second.port, stable); err != nil {
		t.Fatal(err)
	}
	console.stderr.waitFor(t, "Serial device reconnected: "+stable)
	secondOutput := "\x1b[32mafter\x1b[0m\r\n"
	second.send(t, secondOutput)
	console.screen.waitFor(t, secondOutput)
	console.write(t, "after\r")
	second.received.waitFor(t, "after\r")
	if got := second.received.String(); got != "after\r" {
		t.Errorf("replacement received %q; offline input must not be replayed", got)
	}

	if err := os.Remove(stable); err != nil {
		t.Fatal(err)
	}
	if err := second.socat.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	second.socat.wait(t)
	console.stderr.waitMatch(t, "waiting after the second disconnect", func(got string) bool {
		return strings.Count(got, "Waiting for serial device to reconnect:") == 2
	})
	console.write(t, "also-discarded\r")
	console.stderr.waitMatch(t, "input discarded in the second outage", func(got string) bool {
		return strings.Count(got, "Input while disconnected is discarded") == 2
	})
	// Leaving a terminal console adds a presentation newline; inspect its raw output first.
	if got := console.screen.String(); got != firstOutput+secondOutput {
		t.Errorf("console stdout %q, want only the device's bytes %q", got, firstOutput+secondOutput)
	}
	console.write(t, "\x01x")

	res := console.wait(t)
	mustSucceed(t, res)
	if got := first.received.String(); got != "before\r" {
		t.Errorf("original device received %q, want %q", got, "before\r")
	}
	inOrder(t, res.stderr, "Connected to", "Serial device disconnected:",
		"Waiting for", "Input while disconnected is discarded", "Serial device reconnected:",
		"Serial device disconnected:", "Waiting for", "Input while disconnected is discarded", "Connection closed")
	if strings.Count(res.stderr, "Connected to") != 1 || strings.Count(res.stderr, "Connection closed") != 1 {
		t.Errorf("console was reopened or closed more than once: %q", res.stderr)
	}
	if after := pty.Termios(t, console.slave); after.Lflag != before.Lflag || after.Iflag != before.Iflag || after.Oflag != before.Oflag {
		t.Errorf("terminal settings were not restored after exiting while disconnected")
	}
}

func TestSerialConsoleReconnectInitiallyAbsent(t *testing.T) {
	t.Parallel()

	port := filepath.Join(t.TempDir(), "absent-serial-by-id")
	agt := serialAgent(t, port)
	console := agt.startOnTerminal(t, "-no-color", "dut", "script", "-reconnect", "-i")
	before := console.original
	console.stderr.waitFor(t, "Waiting for serial device to reconnect: "+port)
	console.write(t, "offline\r")
	console.stderr.waitFor(t, "Input while disconnected is discarded")
	if got := console.screen.String(); got != "" {
		t.Errorf("absent port produced data: %q", got)
	}
	console.write(t, "\x01x")

	res := console.wait(t)
	mustSucceed(t, res)
	if strings.Contains(res.stderr, "Connected to") {
		t.Errorf("absent port claimed a connection: %q", res.stderr)
	}
	if after := pty.Termios(t, console.slave); after.Lflag != before.Lflag || after.Iflag != before.Iflag || after.Oflag != before.Oflag {
		t.Errorf("terminal settings were not restored after exiting before the first connection")
	}
}

func TestSerialConsoleReconnectFromAPipe(t *testing.T) {
	t.Parallel()

	line := startSerialLine(t)
	agt := serialAgent(t, line.port)
	console := agt.start(t, "dut", "script", "-reconnect", "-i")
	// Feed the pipe without waiting for the connection notice.
	console.write(t, "startup\r")
	console.closeInput(t)

	res := console.wait(t)
	mustSucceed(t, res)
	line.received.waitFor(t, "startup\r")
	if got := line.received.String(); got != "startup\r" {
		t.Errorf("initial piped input = %q, want %q", got, "startup\r")
	}
	if res.stdout != "" || strings.Contains(res.stderr, "discarded") {
		t.Errorf("connected pipe input was discarded or reached stdout: stdout=%q stderr=%q", res.stdout, res.stderr)
	}
	inOrder(t, res.stderr, "Connected to", "Connection closed")
}
