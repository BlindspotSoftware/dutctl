// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"slices"
	"strings"

	"github.com/BlindspotSoftware/dutctl/internal/keyword"

	tea "charm.land/bubbletea/v2"
)

// The line is resolved only at Enter: typing never calls the agent. While it
// holds one token it filters the menu and Enter opens the highlighted row;
// otherwise it is the command line's grammar relative to the prompt's level,
// split on white space (no quoting). The TUI checks no names: the agent
// answers NotFound, and the landing deals with it. The one local check is the
// grammar of lock and unlock, as on the command line.

// enter acts on Enter.
func (m *model) enter() tea.Cmd {
	tokens := strings.Fields(m.input.Value())
	_, entry := m.argEntry()

	if entry || len(tokens) > 1 {
		return m.resolve(tokens)
	}

	if item, ok := m.menu.current(m.rows()); ok {
		return m.open(item)
	}

	if len(tokens) == 1 {
		return m.resolve(tokens)
	}

	return nil
}

// open acts on a highlighted row: hosts and devices are entered, a command or
// a keyword goes into the line for its arguments.
func (m *model) open(item row) tea.Cmd {
	switch item.kind {
	case rowHost:
		return m.connect(item.name, nil)
	case rowRecentDevice:
		return m.connect(item.host, []string{item.name})
	case rowDevice:
		return m.openDevice(item.name, nil)
	case rowCommand:
		m.setLine(item.name + " ")
	}

	return nil
}

// tab completes the highlighted row into the line, else the first row shown,
// as a filtered host is; at the devices level it enters the device at once,
// the line staying empty.
func (m *model) tab() tea.Cmd {
	if _, entry := m.argEntry(); entry {
		return nil
	}

	shown := m.menu.shown(m.rows())

	pos := m.menu.index(shown)
	if pos < 0 {
		pos = firstRow(shown, func(row) bool { return true })
	}

	if pos < 0 {
		return nil
	}

	item := shown[pos]
	if item.kind == rowDevice {
		return m.openDevice(item.name, nil)
	}

	m.setLine(item.name + " ")

	return nil
}

// resolve resolves a typed line from the prompt's level: at the hosts level
// the first token is a recent device or a host, at the devices level a
// device; what follows is carried down as pending and run once the level it
// belongs to is reached.
func (m *model) resolve(tokens []string) tea.Cmd {
	if len(tokens) == 0 {
		return nil
	}

	switch m.level {
	case levelHosts:
		if host, ok := m.hist.hostOf(tokens[0]); ok {
			return m.connect(host, tokens)
		}

		return m.connect(tokens[0], tokens[1:])
	case levelDevices:
		return m.openDevice(tokens[0], tokens[1:])
	default:
		return m.runLine(strings.Join(tokens, " "))
	}
}

// runLine runs a line at the commands level: a keyword, help, or a command.
func (m *model) runLine(line string) tea.Cmd {
	tokens := strings.Fields(line)
	if len(tokens) == 0 {
		return nil
	}

	command, args := tokens[0], tokens[1:]

	switch {
	case command == keyword.Lock:
		return m.lock(line, args)
	case command == keyword.Unlock:
		return m.unlock(line, args)
	case len(args) > 0 && args[0] == keyword.Help:
		if len(args) > 1 {
			return m.printFailure("help takes no arguments: " + command + " help")
		}

		return m.showHelp(line, command)
	}

	return m.startRun(line, command, args)
}

// lock parses the lock keyword's arguments as the command line does; a bad
// duration is refused here, without a call.
func (m *model) lock(line string, args []string) tea.Cmd {
	if len(args) > 1 {
		return m.printFailure("lock takes one duration at most, e.g. lock 2h")
	}

	duration, err := parseLockDuration(args)
	if err != nil {
		return m.printFailure(err.Error())
	}

	return m.startLock(line, duration)
}

// unlock parses the unlock keyword's arguments as the command line does.
func (m *model) unlock(line string, args []string) tea.Cmd {
	force, err := parseUnlockArgs(args)
	if err != nil {
		return m.printFailure("unlock takes nothing or force")
	}

	return m.startUnlock(line, force)
}

// argEntry reports whether the line holds a command and a space after it,
// at the commands level: the arguments are being typed. It returns the
// command.
func (m *model) argEntry() (string, bool) {
	if m.level != levelCommands {
		return "", false
	}

	name, _, spaced := strings.Cut(strings.TrimLeft(m.input.Value(), " "), " ")
	if !spaced {
		return "", false
	}

	if isKeyword(name) || slices.Contains(m.commands, name) {
		return name, true
	}

	return "", false
}

// isKeyword reports whether name is a keyword the TUI takes at a device.
func isKeyword(name string) bool {
	return name == keyword.Lock || name == keyword.Unlock
}

// filterText is what the line filters the menu by: its first token, unless
// arguments are being typed.
func (m *model) filterText() string {
	if _, entry := m.argEntry(); entry {
		return ""
	}

	tokens := strings.Fields(m.input.Value())
	if len(tokens) == 0 {
		return ""
	}

	return tokens[0]
}

// setLine puts text into the line, the cursor at its end.
func (m *model) setLine(text string) {
	m.input.SetValue(text)
	m.input.CursorEnd()
	m.lineChanged()
}

// lineChanged follows an edit of the line: the menu unfolds and filters by
// it, and a note about the line before is gone.
func (m *model) lineChanged() {
	m.note = ""
	m.menu.folded = false
	m.menu.setFilter(m.filterText())
}
