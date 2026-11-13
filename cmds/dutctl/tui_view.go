// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"
)

// Layout of the live view, in columns.
const (
	indent    = 2  // before every row
	lead      = 4  // before a row's name: the indent and the highlight's marker
	gap       = 2  // between columns
	margin    = 1  // the last column stays empty: some terminals wrap there
	minDirCol = 10 // the working directory is dropped rather than cut shorter
)

// theme holds the styles of the live view, the part bubbletea redraws. The
// colours are given for a dark and a light background; bubbletea brings them
// down to what the terminal shows. Teal is the accent, violet is reserved for
// "the device owns the terminal", the rest is the client's palette.
type theme struct {
	accent  lipgloss.Style
	muted   lipgloss.Style
	subtle  lipgloss.Style
	success lipgloss.Style
	warning lipgloss.Style
	failure lipgloss.Style
	console lipgloss.Style
}

// newTheme builds the styles for the background; without colour they carry
// no colour, and the glyphs carry the state.
func newTheme(dark, color bool) theme {
	pick := lipgloss.LightDark(dark)

	paint := func(light, dark string) lipgloss.Style {
		if !color {
			return lipgloss.NewStyle()
		}

		return lipgloss.NewStyle().Foreground(pick(lipgloss.Color(light), lipgloss.Color(dark)))
	}

	return theme{
		accent:  paint("#0F766E", "#2DD4BF"),
		muted:   paint("#6E7781", "#8B949E"),
		subtle:  paint("#D0D7DE", "#3B4048"),
		success: paint("#1A7F37", "#3FB950"),
		warning: paint("#9A6700", "#D29922"),
		failure: paint("#CF222E", "#F85149"),
		console: paint("#7C3AED", "#C084FC"),
	}
}

// tone returns the style of a note's tone.
func (th theme) tone(meaning tone) lipgloss.Style {
	switch meaning {
	case toneSuccess:
		return th.success
	case toneWarning:
		return th.warning
	case toneError:
		return th.failure
	default:
		return th.muted
	}
}

// newInput returns the prompt's line editor: no prompt of its own, the
// breadcrumb is it, and a steady cursor, so the frame changes only on input.
func newInput(styling theme) textinput.Model {
	input := textinput.New()
	input.Prompt = ""

	styles := textinput.DefaultStyles(true)
	styles.Focused.Text = lipgloss.NewStyle()
	styles.Focused.Prompt = lipgloss.NewStyle()
	styles.Cursor.Color = styling.accent.GetForeground()
	styles.Cursor.Blink = false
	input.SetStyles(styles)
	input.Focus()

	return input
}

// View draws the live region under the transcript: the prompt in its box,
// the menu or the help, the footer. While a run holds the terminal, and once
// the TUI quits, it is empty, so nothing of it stays behind. It is never
// taller than the terminal, which would cut it from the top, the box first:
// the help block takes what the menu leaves, and what does not fit goes from
// the end of the middle.
func (m *model) View() tea.View {
	if m.running || m.quitting {
		return tea.NewView("")
	}

	lines, footer := m.box(), m.footer()
	room := max(0, m.height-len(lines)-len(footer))

	var middle []string

	switch {
	case m.overlay:
		middle = m.overlayLines()
	case m.busy != nil:
		middle = []string{pad(indent) + m.spin.View() + " " + m.theme.muted.Render(m.busy.label+" …")}
	default:
		middle = m.menuLines()
		middle = append(middle, m.helpBlock(room-len(middle))...)
	}

	lines = append(lines, middle[:min(len(middle), room)]...)

	return tea.NewView(strings.Join(append(lines, footer...), "\n"))
}

// pad returns n spaces, none for n below 1.
func pad(n int) string {
	return strings.Repeat(" ", max(0, n))
}

// narrow reports whether the right-hand columns are dropped.
func (m *model) narrow() bool {
	return m.width < narrowWidth
}

// usable is the width a row may take.
func (m *model) usable() int {
	return m.width - margin
}

// crumbs returns the breadcrumb's segments: where the prompt is.
func (m *model) crumbs() []string {
	switch m.level {
	case levelDevices:
		return []string{m.host}
	case levelCommands:
		return []string{m.host, m.device}
	default:
		return []string{"dutctl"}
	}
}

// crumb renders the breadcrumb and the prompt glyph within half the width:
// earlier segments muted, the current one in the accent; leading segments
// give way to "…", the last one stays whole.
func (m *model) crumb() string {
	const share = 2

	segs := m.crumbs()
	limit := m.width / share
	cut := false

	for len(segs) > 1 && ansi.StringWidth(strings.Join(segs, " › ")+" › ") > limit {
		segs, cut = segs[1:], true
	}

	var out strings.Builder
	if cut {
		out.WriteString(m.theme.muted.Render("… › "))
	}

	for _, seg := range segs[:len(segs)-1] {
		out.WriteString(m.theme.muted.Render(seg + " › "))
	}

	out.WriteString(m.theme.accent.Render(segs[len(segs)-1]+" ›") + " ")

	return out.String()
}

// boxFrame is what the box takes of a row: two borders, two spaces.
const boxFrame = 4

// fitInput sizes the line editor to what the box leaves of the width; the
// editor's view is one column wider, for the cursor.
func (m *model) fitInput() {
	inner := m.width - boxFrame
	if m.width < bareWidth {
		inner = m.width
	}

	m.input.SetWidth(max(1, inner-1-ansi.StringWidth(m.crumb())))
}

// box draws the prompt: the breadcrumb and the line in a rounded box, bare
// below bareWidth.
func (m *model) box() []string {
	inner := m.crumb() + m.input.View()
	if m.width < bareWidth {
		return []string{ansi.Truncate(inner, m.width, "")}
	}

	room := m.width - boxFrame
	inner = ansi.Truncate(inner, room, "")
	inner += pad(room - ansi.StringWidth(inner))
	edge := strings.Repeat("─", m.width-boxFrame/2)
	border := m.theme.subtle.Render

	return []string{
		border("╭" + edge + "╮"),
		border("│") + " " + inner + " " + border("│"),
		border("╰" + edge + "╯"),
	}
}

// maxMenuRows is how many rows the menu shows at once: never more than the
// height leaves beside the box, the footer and the ↑ ↓ notes.
func (m *model) maxMenuRows() int {
	const (
		narrowRows = 6
		minRows    = 4
		chrome     = 12 // the box, the footer, and room for the transcript's last lines above
		frame      = 7  // the box, the footer on two rows, the ↑ ↓ notes
	)

	limit := max(minRows, m.height-chrome)
	if m.narrow() {
		limit = narrowRows
	}

	return max(1, min(limit, m.height-frame))
}

// menuLines draws the menu: a window of the shown rows around the highlight,
// with notes that count the rows above and below it that can be highlighted;
// folded, the strip.
func (m *model) menuLines() []string {
	shown := m.menu.shown(m.rows())
	if len(shown) == 0 {
		return nil
	}

	high := m.menu.index(shown)

	if _, entry := m.argEntry(); m.menu.folded && !entry {
		return []string{m.strip(shown, high)}
	}

	first, end, title := menuWindow(shown, high, m.maxMenuRows())
	nameCol := nameColumn(shown)

	var lines []string

	more := func(arrow string, hidden []row) {
		if count := selectableRows(hidden); count > 0 {
			lines = append(lines, pad(lead)+m.theme.muted.Render(arrow+" "+strconv.Itoa(count)+" more"))
		}
	}

	more("↑", shown[:first])

	if title >= 0 {
		lines = append(lines, m.rowLine(shown[title], false, nameCol))
	}

	for i := first; i < end; i++ {
		lines = append(lines, m.rowLine(shown[i], i == high, nameCol))
	}

	more("↓", shown[end:])

	return lines
}

// strip draws the folded menu in one line: the highlighted row first, marked
// as in the menu, then the other rows that can be highlighted, in menu order
// and muted, without the section titles; cut at the width.
func (m *model) strip(shown []row, high int) string {
	var (
		first string
		rest  []string
	)

	for pos, item := range shown {
		switch {
		case pos == high:
			first = m.theme.accent.Bold(true).Render("› " + item.name)
		case item.selectable():
			rest = append(rest, item.name)
		}
	}

	others := strings.Join(rest, " · ")
	if first != "" && others != "" {
		others = " · " + others
	}

	return ansi.Truncate(pad(indent)+first+m.theme.muted.Render(others), m.usable(), "…")
}

// menuWindow returns the rows of shown the menu draws, from first to end, at
// most limit with the highlight at high among them, and the position of a
// title to draw before them, or -1. A window never starts inside a section
// without its title: its first row's place goes to the title, or to nothing
// when the next section starts after it. Nor does it end on a title.
func menuWindow(shown []row, high, limit int) (int, int, int) {
	start := 0
	if high >= limit {
		start = high - limit + 1
	}

	end := min(len(shown), start+limit)
	for end > start+1 && shown[end-1].kind == rowHeader {
		end--
	}

	if start == 0 || start >= high || shown[start].kind == rowHeader {
		return start, end, -1
	}

	if shown[start+1].kind == rowHeader {
		return start + 1, end, -1
	}

	return start + 1, end, sectionTitle(shown, start)
}

// sectionTitle returns the position of the title of the section the row at
// pos is in, or -1.
func sectionTitle(shown []row, pos int) int {
	for i := pos - 1; i >= 0; i-- {
		if shown[i].kind == rowHeader {
			return i
		}
	}

	return -1
}

// selectableRows counts the rows that can be highlighted.
func selectableRows(rows []row) int {
	count := 0

	for _, item := range rows {
		if item.selectable() {
			count++
		}
	}

	return count
}

// nameColumn is the width of the name column: the longest name and a gap.
func nameColumn(rows []row) int {
	const (
		minCol = 16
		maxCol = 28
	)

	width := minCol

	for _, item := range rows {
		if item.selectable() {
			width = max(width, ansi.StringWidth(item.name)+gap)
		}
	}

	return min(width, maxCol)
}

// rowCells are the parts of a row, plain, before they are fitted and styled.
type rowCells struct {
	name, desc, note, noteMark string
}

// cells returns the parts of a row; a narrow terminal keeps the name and the
// glyph only.
func (m *model) cells(item row) rowCells {
	cells := rowCells{name: item.name, desc: item.desc, note: item.note}

	if item.raw {
		cells.noteMark = " ⌨"
	}

	if m.narrow() {
		cells.desc, cells.note, cells.noteMark = "", item.glyph, ""
	}

	return cells
}

// rowLine draws one row: marker, name, description, and the note
// right-aligned; what does not fit goes, the description first.
func (m *model) rowLine(item row, highlighted bool, nameCol int) string {
	styling := m.theme

	if item.kind == rowHeader {
		return pad(indent) + styling.muted.Render(ansi.Truncate(item.name, m.usable()-indent, "…"))
	}

	marker, nameStyle := pad(indent), lipgloss.NewStyle()
	if highlighted {
		marker, nameStyle = styling.accent.Bold(true).Render("› "), styling.accent.Bold(true)
	}

	cells := m.cells(item)
	used := lead

	cells.name = ansi.Truncate(cells.name, m.usable()-used, "…")
	used += ansi.StringWidth(cells.name)
	space := max(1, nameCol-ansi.StringWidth(cells.name))

	if cells.desc != "" {
		// Only a recent device has both columns: its host tells which agent
		// Enter dials, so its last line gives way.
		fit := m.usable() - used - space - ansi.StringWidth(cells.desc) - gap - ansi.StringWidth(cells.noteMark)
		cells.note = ansi.Truncate(cells.note, max(0, fit), "…")
	}

	noteWidth := ansi.StringWidth(cells.note + cells.noteMark)
	if noteWidth > 0 && used+space+gap+noteWidth > m.usable() {
		cells.note, cells.noteMark, noteWidth = "", "", 0
	}

	room := m.usable() - used - space
	if noteWidth > 0 {
		room -= gap + noteWidth
	}

	cells.desc = ansi.Truncate(cells.desc, max(0, room), "…")

	out := pad(indent) + marker + nameStyle.Render(cells.name)
	if cells.desc != "" {
		out += pad(space) + styling.muted.Render(cells.desc)
		used += space + ansi.StringWidth(cells.desc)
	}

	if noteWidth > 0 {
		out += pad(max(gap, m.usable()-used-noteWidth)) + styling.tone(item.tone).Render(cells.note) + styling.console.Render(cells.noteMark)
	}

	return out
}

// Help block sizes, in lines.
const (
	helpEntryLines  = 8
	helpNarrowLines = 2
)

// helpBlock draws the ╎ block while the arguments of a command are typed: its
// help after the first line, which is the description already, shorter on a
// narrow terminal, and at most room lines. A help cut short says how to see
// all of it. The menu folded after a run shows none.
func (m *model) helpBlock(room int) []string {
	name, entry := m.argEntry()
	if !entry || m.menu.folded || room < 1 {
		return nil
	}

	limit := helpEntryLines
	if m.narrow() {
		limit = helpNarrowLines
	}

	limit = min(limit, room)

	body := helpBody(m.help[name])
	if len(body) > limit {
		body = append(trimBlank(body[:limit-1]), "… "+name+" help prints all of it")
	}

	lines := make([]string, 0, len(body))
	rule := pad(indent) + m.theme.subtle.Render("╎") + " "

	for _, text := range body {
		lines = append(lines, rule+m.theme.muted.Render(ansi.Truncate(text, m.usable()-lead, "…")))
	}

	return lines
}

// helpBody returns the lines of help after its first non-empty line; blank
// lines never come two in a row, lead or trail.
func helpBody(help string) []string {
	var out []string

	started := false

	for text := range strings.SplitSeq(strings.ReplaceAll(help, "\t", "  "), "\n") {
		text = strings.TrimRight(text, " ")

		switch {
		case !started:
			started = text != ""

			continue
		case text == "" && (len(out) == 0 || out[len(out)-1] == ""):
			continue
		}

		out = append(out, text)
	}

	return trimBlank(out)
}

// trimBlank drops the blank lines at the end of lines.
func trimBlank(lines []string) []string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	return lines
}

// Key hints shown in the footer.
const (
	hintKeys     = "? keys"
	hintComplete = "tab complete"
	hintRefresh  = "ctrl+r refresh"
)

// keyHints returns the keys that act now, the most important first.
func (m *model) keyHints() []string {
	_, entry := m.argEntry()

	switch {
	case m.overlay:
		return []string{"any key closes this"}
	case m.busy != nil:
		return []string{"esc cancels", "ctrl+d quits"}
	case entry:
		return []string{"enter run", "esc clear", hintKeys}
	case m.menu.folded:
		return []string{"enter pick", "↑↓ list", hintComplete, "esc devices", hintKeys}
	case m.level == levelDevices:
		return []string{"enter open", hintComplete, "esc hosts", hintRefresh, hintKeys}
	case m.level == levelCommands:
		return []string{"enter pick", hintComplete, "esc devices", hintRefresh, hintKeys}
	default:
		return []string{"enter open", hintComplete, "↑↓ move", "type to filter", hintKeys}
	}
}

// footerNote is what replaces the first key hint: a note the TUI set, or
// "no match" for a filter that hides every command. At a host and at the
// hosts, Enter opens a name no row has, so "enter open" stays.
func (m *model) footerNote() string {
	if m.note != "" || m.menu.filter == "" || m.busy != nil || m.overlay || m.level != levelCommands {
		return m.note
	}

	if _, ok := m.menu.current(m.rows()); !ok {
		return "no match"
	}

	return ""
}

// hintText renders the key hints, a note in place of the first.
func (m *model) hintText(hints []string, note string) string {
	if note == "" {
		return m.theme.muted.Render(strings.Join(hints, " · "))
	}

	out := m.theme.warning.Render(note)
	if len(hints) > 1 {
		out += m.theme.muted.Render(" · " + strings.Join(hints[1:], " · "))
	}

	return out
}

// footer draws the key hints and the status in one row: the status keeps the
// lock state before the hints keep their middle, and the directory gives way
// first. On a narrow terminal the two take a row each and the hints are
// their keys only.
func (m *model) footer() []string {
	hints, note := m.keyHints(), m.footerNote()
	parts := m.status()
	room := m.usable() - indent

	if m.narrow() {
		for i := 1; i < len(hints)-1; i++ {
			hints[i] = strings.Fields(hints[i])[0]
		}

		status, _ := m.statusText(parts, room)

		return []string{
			pad(indent) + ansi.Truncate(m.hintText(hints, note), room, "…"),
			pad(indent) + status,
		}
	}

	for {
		left := m.hintText(hints, note)
		status, kept := m.statusText(parts, room-ansi.StringWidth(left)-gap)

		if kept >= len(parts)-1 || len(hints) <= 2 {
			return []string{pad(indent) + left + pad(room-ansi.StringWidth(left)-ansi.StringWidth(status)) + status}
		}

		hints = append(hints[:len(hints)-2], hints[len(hints)-1])
	}
}

// statusPart is one part of the footer's status, with its style.
type statusPart struct {
	text  string
	style lipgloss.Style
	dir   bool // the working directory: cut from the left before it goes
}

// status returns the footer's status: the user, at a device its lock state,
// the working directory.
func (m *model) status() []statusPart {
	parts := []statusPart{{text: m.user, style: m.theme.muted}}

	if m.level == levelCommands {
		text, meaning := "free", toneMuted

		for _, entry := range m.devices {
			if entry.Name == m.device && entry.Locked {
				text, meaning = m.lockNote(entry)
			}
		}

		parts = append(parts, statusPart{text: text, style: m.theme.tone(meaning)})
	}

	if m.cwd != "" {
		parts = append(parts, statusPart{text: m.cwd, style: m.theme.muted, dir: true})
	}

	return parts
}

// statusText renders the status within room columns: the directory cut from
// the left, else dropped, then the parts before it from the last. It returns
// how many parts it kept.
func (m *model) statusText(parts []statusPart, room int) (string, int) {
	const sep = " · "

	for kept := len(parts); kept > 0; kept-- {
		shown := parts[:kept]

		width := (len(shown) - 1) * ansi.StringWidth(sep)
		for _, part := range shown {
			width += ansi.StringWidth(part.text)
		}

		last := shown[kept-1]
		if over := width - room; over > 0 {
			if !last.dir || ansi.StringWidth(last.text)-over-1 < minDirCol {
				continue
			}

			shown = append(shown[:kept-1:kept-1], statusPart{text: ansi.TruncateLeft(last.text, over+1, "…"), style: last.style})
		}

		var out strings.Builder

		for i, part := range shown {
			if i > 0 {
				out.WriteString(m.theme.muted.Render(sep))
			}

			out.WriteString(part.style.Render(part.text))
		}

		return out.String(), kept
	}

	return "", 0
}

// overlayLines draws the help overlay: the keys, the lines one types, and
// how consoles end, one under the other so it reads at any width. Ctrl-A has
// two meanings, at the prompt and in a raw console; both are there.
func (m *model) overlayLines() []string {
	sections := []struct {
		title string
		rows  [][2]string
	}{
		{"keys", [][2]string{
			{"enter", "open the row / run the line"},
			{"tab", "complete the row into the line"},
			{"↑ ↓", "move the highlight"},
			{"esc", "clear the line, else one up"},
			{"ctrl+r", "refresh this level"},
			{"ctrl+c", "clear the line; twice: quit"},
			{"ctrl+d", "quit (empty line)"},
			{"ctrl+a e u w k", "edit the line"},
		}},
		{"lines (type, then enter)", [][2]string{
			{"power on", "run on this device"},
			{"demo-board power on", "a device first works at every level"},
			{"lab:2024", "connect to a host, at the hosts"},
			{"lock 2h", "reserve the device"},
			{"unlock force", "release it, also another's"},
			{"power help", "a command's full help"},
		}},
		{"consoles", [][2]string{
			{"line", "Enter sends a line · Ctrl-D ends input · Ctrl-C ends the run"},
			{"raw", "Ctrl-A x ends it · Ctrl-A twice sends Ctrl-A · Ctrl-A e echo"},
		}},
	}

	const keyCol = 22

	var lines []string

	for _, section := range sections {
		lines = append(lines, pad(indent)+m.theme.muted.Render(ansi.Truncate(section.title, m.usable()-indent, "…")))

		for _, item := range section.rows {
			text := m.theme.accent.Render(item[0]) + pad(keyCol-ansi.StringWidth(item[0])) + item[1]
			lines = append(lines, pad(indent)+ansi.Truncate(text, m.usable()-indent, "…"))
		}
	}

	return lines
}
