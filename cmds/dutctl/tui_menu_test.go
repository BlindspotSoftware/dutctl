// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"
)

func menuRows() []row {
	return []row{
		{kind: rowHeader, name: "Commands"},
		{kind: rowCommand, name: "console"},
		{kind: rowCommand, name: "Power"},
		{kind: rowCommand, name: "protect"},
		{kind: rowHeader, name: "Device"},
		{kind: rowCommand, name: "lock"},
		{kind: rowCommand, name: "unlock"},
	}
}

func names(rows []row) string {
	out := make([]string, 0, len(rows))
	for _, item := range rows {
		out = append(out, item.name)
	}

	return strings.Join(out, ",")
}

// The filter is a case-insensitive substring of the name, of a keyword too; a
// section shows only with a row.
func TestMenuFilter(t *testing.T) {
	mn := menu{prefer: rowCommand}

	for filter, want := range map[string]string{
		"":    "Commands,console,Power,protect,Device,lock,unlock",
		"POW": "Commands,Power",
		"pro": "Commands,protect",
		"lo":  "Device,lock,unlock",
		"zz":  "",
	} {
		mn.setFilter(filter)

		if got := names(mn.shown(menuRows())); got != want {
			t.Errorf("filter %q shows %q, want %q", filter, got, want)
		}
	}
}

// The highlight starts on the first command, never on a keyword while a
// command is shown, an exact name wins, and moves skip the titles and stop at
// the ends.
func TestMenuHighlight(t *testing.T) {
	rows := menuRows()
	mn := menu{prefer: rowCommand}

	for filter, want := range map[string]string{"": "console", "power": "Power", "o": "console", "lock": "lock", "lo": "lock"} {
		mn.setFilter(filter)

		if item, _ := mn.current(rows); item.name != want {
			t.Errorf("filter %q highlights %q, want %q", filter, item.name, want)
		}
	}

	mn.setFilter("")
	mn.move(rows, 3)

	if item, _ := mn.current(rows); item.name != "lock" {
		t.Errorf("three down from the first command highlights %q, want lock past the title", item.name)
	}

	mn.move(rows, 10)

	if item, _ := mn.current(rows); item.name != "unlock" {
		t.Errorf("down past the last row highlights %q", item.name)
	}

	mn.move(rows, -10)

	if item, _ := mn.current(rows); item.name != "console" {
		t.Errorf("up past the first row highlights %q", item.name)
	}
}

// commandsView renders the commands level of a TUI without colour at
// width.
func commandsView(t *testing.T, width int) *harness {
	t.Helper()

	h := newHarness(t, tuiFake())
	h.m.ink, h.m.cwd = false, "~/fw"
	h.m.restyle()
	h.atCommands()
	h.update(tea.WindowSizeMsg{Width: width, Height: 30})

	return h
}

// The layout at 100 and 60 columns, pinned.
func TestTUIViewGolden(t *testing.T) {
	wide := strings.Join([]string{
		"╭──────────────────────────────────────────────────────────────────────────────────────────────────╮",
		"│ lab:2024 › demo-board ›                                                                          │",
		"╰──────────────────────────────────────────────────────────────────────────────────────────────────╯",
		"  Commands",
		"  › power           Switches the power.",
		"    console         Switches the power.",
		"  Device",
		"    lock            reserve the device for you: lock [duration], e.g. lock 2h",
		"    unlock          release your reservation; unlock force releases another's",
		"  enter pick · tab complete · esc devices · ? keys    ada@lab · locked by \"bob@desk\" for 25m · ~/fw",
	}, "\n")
	narrow := strings.Join([]string{
		"╭──────────────────────────────────────────────────────────╮",
		"│ lab:2024 › demo-board ›                                  │",
		"╰──────────────────────────────────────────────────────────╯",
		"  Commands",
		"  › power",
		"    console",
		"  Device",
		"    lock",
		"    unlock",
		"  enter pick · tab · esc · ctrl+r · ? keys",
		"  ada@lab · locked by \"bob@desk\" for 25m · ~/fw",
	}, "\n")

	for width, want := range map[int]string{100: wide, 60: narrow} {
		if got := commandsView(t, width).view(); got != want {
			t.Errorf("view at %d columns:\n%s\nwant:\n%s", width, got, want)
		}
	}
}

// A short terminal keeps the box with the line typed into: the help, then the
// menu, give way. A window scrolled into a section never shows its rows
// without the title, and its notes count rows that can be highlighted only.
func TestTUIViewFitsTheHeight(t *testing.T) {
	for _, height := range []int{12, 8, 6} {
		h := commandsView(t, 100)
		h.update(tea.WindowSizeMsg{Width: 100, Height: height})

		for _, state := range []func(){
			func() {},
			func() { h.press("down") },
			func() { h.typ("power ") },
			func() { h.press("esc", "?") },
			func() { h.press("x"); h.runPower(nil) },
		} {
			state()

			lines := strings.Split(h.view(), "\n")
			if len(lines) > height || !strings.Contains(lines[1], "demo-board ›") {
				t.Errorf("at %d rows the view has %d lines, the line typed into second:\n%s", height, len(lines), h.view())
			}
		}
	}

	h := commandsView(t, 100)
	h.update(tea.WindowSizeMsg{Width: 100, Height: 12})
	h.press("down", "down")

	if view := h.view(); !strings.Contains(view, "    ↑ 1 more\n  Commands\n    console") || !strings.Contains(view, "↓ 1 more") {
		t.Errorf("the window after two moves at 12 rows:\n%s", view)
	}
}

// The name column follows the names beside a column, and a recent device's
// host stays whole: its last line gives way.
func TestTUIViewColumns(t *testing.T) {
	long := strings.Repeat("x ", 40)
	h := newHarness(t, tuiFake(),
		"1794470490\tlab-with-a-long-name.example.org:2024\tdemo-board\techo "+long+"\t",
		"1794470490\tlab:2024\tdemo-board\tpower "+long+"\t",
	)

	if view := h.view(); !strings.Contains(view, "lab-with-a-long-name.example.org:2024  just now · echo x") {
		t.Errorf("the recent device's host is cut:\n%s", view)
	}

	h.atCommands()

	if view := h.view(); !strings.Contains(view, "› power           Switches the power.") {
		t.Errorf("a long recent line moved the description column:\n%s", view)
	}
}

// No line is wider than the terminal, at any level and width; without colour
// the view sets no colour.
func TestTUIViewFitsTheWidth(t *testing.T) {
	for _, width := range []int{100, 60, 39, 20} {
		h := commandsView(t, width)

		for _, state := range []func(){
			func() {},
			func() { h.typ("power ") },
			func() { h.press("esc", "?") },
			func() { h.press("x"); h.runPower(nil) },
			func() { h.press("x", "esc", "esc") },
			func() { h.press("esc", "esc") },
		} {
			state()

			content := h.m.View().Content
			for _, line := range strings.Split(content, "\n") {
				if w := ansi.StringWidth(line); w > width {
					t.Errorf("at %d columns, level %d, a line is %d wide: %q", width, h.m.level, w, ansi.Strip(line))
				}
			}

			if strings.Contains(content, "\x1b[3") || strings.Contains(content, "\x1b[9") || strings.Contains(content, ";2;") {
				t.Errorf("at %d columns without colour, the view sets a colour: %q", width, content)
			}
		}
	}
}
