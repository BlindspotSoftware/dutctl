// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/BlindspotSoftware/dutctl/internal/style"
)

// splashNotice says, while the TUI is a preview, that it may change and that
// the command line shown above it works as usual.
const splashNotice = "▌ EXPERIMENTAL  The dutctl TUI is a preview: it may change or go away.\n" +
	"▌ You can use dutctl as usual, as shown above; see `dutctl -h` for details."

// splash writes the synopsis of the command line, the one dutctl -h prints,
// and under it the notice that the TUI is experimental, before the TUI
// starts, so both stay on screen above the greeting. The synopsis is gray and
// the notice yellow, its first word bold also without colour.
func (app *application) splash(w io.Writer) {
	paint := ink(!app.noColor)

	for line := range strings.SplitSeq(strings.Trim(usageSynopsis, "\n"), "\n") {
		fmt.Fprintln(w, paint.paint(style.Gray, expandTabs(line)))
	}

	fmt.Fprintln(w)

	for line := range strings.SplitSeq(splashNotice, "\n") {
		if rest, first := strings.CutPrefix(line, "▌ EXPERIMENTAL"); first {
			line = paint.paint(style.Yellow, "▌ ") + strong(paint.paint(style.Yellow, "EXPERIMENTAL")) + paint.paint(style.Yellow, rest)
		} else {
			line = paint.paint(style.Yellow, line)
		}

		fmt.Fprintln(w, line)
	}
}

// expandTabs replaces the tabs of an ASCII line with spaces up to the next
// tab stop, every eight columns, as a terminal shows them.
func expandTabs(line string) string {
	const tabStop = 8

	var out strings.Builder

	for _, char := range line {
		if char == '\t' {
			out.WriteString(pad(tabStop - out.Len()%tabStop))

			continue
		}

		out.WriteRune(char)
	}

	return out.String()
}
