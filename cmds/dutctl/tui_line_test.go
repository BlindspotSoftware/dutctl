// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"slices"
	"strings"
	"testing"
)

// A typed line is resolved at Enter from the prompt's level: what follows a
// host or a device is carried down and run there, in one Enter.
func TestTUIResolvesALine(t *testing.T) {
	recent := "1794470400\tlab:2024\tdemo-board\t\t"

	tests := []struct {
		name     string
		history  []string
		at       level
		line     string
		hosts    []string // clients made, in order
		commands []string // Commands calls
		run      string   // the command run, with its arguments
	}{
		{name: "a command at the device", at: levelCommands, line: "power on", run: "power on"},
		{name: "a device first at the host", at: levelDevices, line: "demo-board power on", commands: []string{"demo-board"}, run: "power on"},
		{
			name: "a recent device at the start", history: []string{recent}, at: levelHosts, line: "demo-board power on",
			hosts: []string{"lab:2024"}, commands: []string{"demo-board"}, run: "power on",
		},
		{
			name: "a host and a device at the start", at: levelHosts, line: "bench demo-board power",
			hosts: []string{"bench:2024"}, commands: []string{"demo-board"}, run: "power",
		},
		{name: "a host at the start", at: levelHosts, line: "bench:2024", hosts: []string{"bench:2024"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tuiFake(), tt.history...)

			switch tt.at {
			case levelCommands:
				h.atCommands()
			case levelDevices:
				h.press("enter")
			case levelHosts:
			}

			h.fake.commandsCalls, h.hosts = nil, nil

			h.typ(tt.line)
			h.press("enter")

			if !slices.Equal(h.hosts, tt.hosts) {
				t.Errorf("clients made for %v, want %v", h.hosts, tt.hosts)
			}

			if !slices.Equal(h.fake.commandsCalls, tt.commands) {
				t.Errorf("Commands calls %v, want %v", h.fake.commandsCalls, tt.commands)
			}

			ran := ""
			if h.m.run != nil {
				ran = strings.Join(append([]string{h.m.run.command}, h.m.run.args...), " ")
			}

			if ran != tt.run {
				t.Errorf("ran %q, want %q", ran, tt.run)
			}
		})
	}
}

// A typed host is dialled as typed, even when a known host contains it; Tab
// completes the known host, and a move highlights it, for Enter to open.
func TestTUIDialsATypedHost(t *testing.T) {
	known := "1794470400\t192.168.1.10:2024\t\t\t"

	for name, keys := range map[string][]string{
		"typed":     {"enter"},
		"completed": {"tab", "enter"},
		"moved":     {"down", "enter"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, tuiFake(), known)
			h.typ("192.168.1.1")
			h.press(keys...)

			want := "192.168.1.10:2024"
			if name == "typed" {
				want = "192.168.1.1:2024"
			}

			if h.m.level != levelDevices || h.m.host != want {
				t.Errorf("at level %d of %q, want the devices of %q", h.m.level, h.m.host, want)
			}
		})
	}
}

// The keywords are parsed as on the command line; a bad duration is refused
// without a call.
func TestTUIKeywords(t *testing.T) {
	h := newHarness(t, tuiFake())
	h.atCommands()

	h.typ("lock 2h")
	h.press("enter")

	if len(h.fake.lockReqs) != 1 || h.fake.lockReqs[0].GetDurationSeconds() != 7200 {
		t.Fatalf("lock 2h: Lock requests %v", h.fake.lockReqs)
	}

	h.mustPrint("● demo-board lock 2h")
	h.mustPrint("⎿ ✓ locked by you for 2h")

	h.typ("unlock force")
	h.press("enter")

	if want := []unlockCall{{device: "demo-board", force: true}}; !equalUnlock(h.fake.unlockCalls, want) {
		t.Errorf("unlock force: Unlock calls %v", h.fake.unlockCalls)
	}

	h.typ("power help")
	h.press("enter")

	if len(h.fake.detailsCalls) == 0 || h.fake.detailsCalls[len(h.fake.detailsCalls)-1].keyword != "help" {
		t.Errorf("power help: Details calls %v", h.fake.detailsCalls)
	}

	h.mustPrint("ARGUMENTS:")

	for _, bad := range []string{"lock banana", "lock 1h 2h", "unlock now", "power help me"} {
		h.typ(bad)
		h.press("enter")
		h.press("esc")
	}

	if len(h.fake.lockReqs) != 1 || len(h.fake.unlockCalls) != 1 || h.execs != 0 {
		t.Errorf("a bad keyword line called the agent: %d locks, %d unlocks, %d runs",
			len(h.fake.lockReqs), len(h.fake.unlockCalls), h.execs)
	}

	h.mustPrint(`✗ invalid lock duration "banana"`)
}

// A space after a command's name, or a keyword, starts its arguments: the
// menu shows that command alone.
func TestTUIArgumentEntry(t *testing.T) {
	h := newHarness(t, tuiFake())
	h.atCommands()

	for _, tt := range []struct {
		line  string
		entry bool
	}{
		{"power", false}, {"power ", true}, {"  power on", true}, {"lock ", true}, {"nothing ", false},
	} {
		h.m.setLine(tt.line)

		if _, entry := h.m.argEntry(); entry != tt.entry {
			t.Errorf("argEntry(%q) = %v, want %v", tt.line, entry, tt.entry)
		}
	}

	h.m.setLine("power ")

	if rows := h.m.rows(); len(rows) != 1 || rows[0].name != "power" {
		t.Errorf("rows while typing power's arguments: %v", rows)
	}
}
