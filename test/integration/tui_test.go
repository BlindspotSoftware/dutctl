// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package integration

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// tuiScreen shows what a terminal shows of a client's output: the bytes
// go through a terminal emulator, which the TUI's redrawn view needs.
type tuiScreen struct {
	emu *vt.Emulator
	fed int // how much of the output the emulator has seen
}

func newTUIScreen() *tuiScreen {
	emu := vt.NewEmulator(termCols, termRows)
	// The emulator's answers to the client's queries are dropped; the copy
	// outlives the test.
	go func() { _, _ = io.Copy(io.Discard, emu) }()

	return &tuiScreen{emu: emu}
}

// shows returns a match for waitMatch: the scrollback and the screen hold
// every one of wants.
func (s *tuiScreen) shows(wants ...string) func(string) bool {
	return func(output string) bool {
		_, _ = s.emu.WriteString(output[s.fed:])
		s.fed = len(output)

		var text strings.Builder
		for _, line := range s.emu.Scrollback().Lines() {
			text.WriteString(line.String() + "\n")
		}

		text.WriteString(s.emu.String())

		for _, want := range wants {
			if !strings.Contains(text.String(), want) {
				return false
			}
		}

		return true
	}
}

// A bare dutctl on a terminal opens the TUI: a typed line runs on the
// device, a raw console takes the terminal and gives it back, Ctrl-C ends a
// run in a line console but not the TUI, and ctrl+d quits. On a pipe, or
// with a structured format, a bare dutctl lists as before.
func TestTUI(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))
	term := agt.startOnTerminalEnv(t, []string{"TERM=xterm-256color"})
	screen := newTUIScreen()

	wait := func(wants ...string) {
		t.Helper()
		term.screen.waitMatch(t, fmt.Sprintf("%q on the screen", wants), screen.shows(wants...))
	}

	wait(agt.addr+" · 2 devices", "esc hosts")

	term.write(t, "demo-board power on\r")
	wait("● demo-board power on", "⎿ done", "demo-board ›", "› power · ")

	term.write(t, "console \r")
	wait("▌ raw console", "demo#")
	term.write(t, "help\r")
	wait("Commands: help")
	term.write(t, "\x01x")
	wait("▌ raw console closed", "· raw console", "› console · ")

	term.write(t, "\x1b")
	wait("esc hosts")
	term.write(t, "agent prompt\r")
	wait("▌ line console", "What is your name?")
	term.write(t, "\x03")
	wait("▌ line console closed", "⎿ interrupted", "agent ›")

	term.write(t, "\x04")
	mustSucceed(t, term.wait(t))

	list := agt.dutctl(t, "list")
	mustSucceed(t, list)

	if piped := agt.dutctl(t); piped.code != 0 || piped.stdout != list.stdout {
		t.Errorf("a bare dutctl on a pipe printed %q (exit %d), want the list %q", piped.stdout, piped.code, list.stdout)
	}

	asJSON := agt.startOnTerminalEnv(t, []string{"TERM=xterm-256color"}, "-f", "json")
	res := asJSON.wait(t)
	mustSucceed(t, res)

	if !strings.Contains(res.stdout, `"contentType": "device-list"`) {
		t.Errorf("a bare dutctl -f json on a terminal printed:\n%s", res.stdout)
	}
}
