// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "strings"

// rowKind tells what a menu row stands for, and so what Enter and Tab do on
// it.
type rowKind int

const (
	rowHeader       rowKind = iota // a section title, never highlighted
	rowRecentDevice                // a device used before: its host and name
	rowHost                        // an agent's address
	rowDevice                      // a device of the current host
	rowCommand                     // a command of the current device, or a keyword
)

// tone is what a row's note means; the theme picks the colour.
type tone int

const (
	toneMuted tone = iota
	toneSuccess
	toneWarning
	toneError
)

// row is one line of a menu.
type row struct {
	kind  rowKind
	name  string // what the row opens, and what Tab puts into the line
	host  string // a recent device's host
	desc  string // the middle column: a command's first help line, a recent device's host
	note  string // the right-hand column: lock state, age, last line
	glyph string // the note in one character, for a narrow terminal
	tone  tone
	raw   bool // the last run opened a raw console: the ⌨ mark
}

func (r row) selectable() bool {
	return r.kind != rowHeader
}

// rowKey names a row across rebuilds of the rows: the highlight sticks to the
// row, not to a position, while descriptions arrive and filters change.
type rowKey struct {
	kind       rowKind
	name, host string
}

func (r row) key() rowKey {
	return rowKey{kind: r.kind, name: r.name, host: r.host}
}

// menu is the state of the list under the prompt: the filter typed and the
// highlighted row. The rows themselves are built from the model's state for
// every use, so they are never out of date.
type menu struct {
	filter string
	sel    rowKey
	// prefer is the kind of row the highlight starts on: at a device the
	// first command, which comes before the keywords.
	prefer rowKind
	// folded draws the menu as one line after the TUI printed the outcome
	// of a line, so the output above it stays in sight; a key that types or
	// moves unfolds it.
	folded bool
}

// shown returns the rows that match the filter, case-insensitive substring
// on the name, with the title of every section that kept a row.
func (mn *menu) shown(rows []row) []row {
	filter := strings.ToLower(mn.filter)

	var (
		out    []row
		header *row
	)

	for i := range rows {
		item := rows[i]

		switch {
		case item.kind == rowHeader:
			header = &rows[i]

			continue
		case !strings.Contains(strings.ToLower(item.name), filter):
			continue
		}

		if header != nil {
			out = append(out, *header)
			header = nil
		}

		out = append(out, item)
	}

	return out
}

// index returns the position in shown of the highlighted row: the selected
// one, else a row named as the filter, else the first of the preferred
// kind, else the first; -1 without any. Only a safe row gets there without a
// move.
func (mn *menu) index(shown []row) int {
	for pos, item := range shown {
		if item.selectable() && item.key() == mn.sel {
			return pos
		}
	}

	exact := func(item row) bool { return mn.filter != "" && strings.EqualFold(item.name, mn.filter) }

	for _, match := range []func(row) bool{
		func(item row) bool { return exact(item) && item.kind == mn.prefer },
		func(item row) bool { return exact(item) && mn.safe(item) },
		func(item row) bool { return item.kind == mn.prefer },
		mn.safe,
	} {
		if pos := firstRow(shown, match); pos >= 0 {
			return pos
		}
	}

	return -1
}

// safe reports whether item may be highlighted without a move. A host under a
// filter may not: the hosts are an open set, and a typed address that only
// contains a known one, as 192.168.1.1 does 192.168.1.10, must be dialled as
// typed.
func (mn *menu) safe(item row) bool {
	return mn.filter == "" || item.kind != rowHost
}

// firstRow returns the position of the first row that can be highlighted
// and matches, or -1.
func firstRow(shown []row, match func(row) bool) int {
	for pos, item := range shown {
		if item.selectable() && match(item) {
			return pos
		}
	}

	return -1
}

// current returns the highlighted row.
func (mn *menu) current(rows []row) (row, bool) {
	shown := mn.shown(rows)

	pos := mn.index(shown)
	if pos < 0 {
		return row{}, false
	}

	return shown[pos], true
}

// move moves the highlight by delta rows that can be highlighted, stopping
// at the first and the last.
func (mn *menu) move(rows []row, delta int) {
	shown := mn.shown(rows)

	step := 1
	if delta < 0 {
		step, delta = -1, -delta
	}

	pos := mn.index(shown)
	if pos < 0 {
		pos = outside(step, len(shown))
	}

	for next := pos + step; delta > 0 && next >= 0 && next < len(shown); next += step {
		if shown[next].selectable() {
			pos = next
			delta--
		}
	}

	if pos >= 0 && pos < len(shown) {
		mn.sel = shown[pos].key()
	}
}

// outside is where a move starts without a highlight: before the first row
// for a move down, after the last for a move up.
func outside(step, rows int) int {
	if step > 0 {
		return -1
	}

	return rows
}

// setFilter filters by text; a new filter puts the highlight back on the
// preferred row.
func (mn *menu) setFilter(text string) {
	if text != mn.filter {
		mn.filter = text
		mn.sel = rowKey{}
	}
}

// choose highlights the row of kind named name, when it is shown.
func (mn *menu) choose(kind rowKind, name string) {
	mn.sel = rowKey{kind: kind, name: name}
}
