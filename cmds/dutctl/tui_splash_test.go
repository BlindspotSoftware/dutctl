// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/BlindspotSoftware/dutctl/internal/style"
	"github.com/charmbracelet/x/ansi"
)

// The splash holds every non-empty line of the synopsis dutctl -h prints, tabs
// expanded, then an empty line and the notice, no line of it wider than 78
// columns; the synopsis is gray and the notice yellow, EXPERIMENTAL bold also
// without colour.
func TestSplash(t *testing.T) {
	var want []string

	for line := range strings.SplitSeq(usageSynopsis, "\n") {
		if line != "" {
			want = append(want, expandTabs(line))
		}
	}

	notice := strings.Split(splashNotice, "\n")
	want = append(append(want, ""), notice...)

	for _, noColor := range []bool{false, true} {
		var out strings.Builder

		app := &application{noColor: noColor}
		app.splash(&out)

		got := strings.Split(strings.TrimSuffix(ansi.Strip(out.String()), "\n"), "\n")
		if !slices.Equal(got, want) || !slices.Contains(got, "        dutctl [options] list") {
			t.Fatalf("no colour %v: the splash\n%s\nwant the synopsis, tabs expanded, then the notice\n%s",
				noColor, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}

		for _, line := range notice {
			if width := ansi.StringWidth(line); width > 78 {
				t.Errorf("the notice line %q is %d columns wide", line, width)
			}
		}

		text := out.String()
		if !strings.Contains(text, bold) || noColor == strings.Contains(text, style.Yellow) || noColor == strings.Contains(text, style.Gray) {
			t.Errorf("no colour %v: the splash's styles in %q", noColor, text)
		}
	}
}
