// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoryPath(t *testing.T) {
	tests := []struct {
		name, state, home, want string
	}{
		{"state home", "/state", "/home/ada", "/state/dutctl/history"},
		{"home", "", "/home/ada", "/home/ada/.local/state/dutctl/history"},
		{"relative state home is ignored", "state", "/home/ada", "/home/ada/.local/state/dutctl/history"},
		{"neither", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", tt.state)
			t.Setenv("HOME", tt.home)

			if got := historyPath(); got != tt.want {
				t.Errorf("historyPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Lines it cannot parse are skipped, missing columns are empty, extra ones
// ignored; a missing file is an empty history.
func TestReadHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	writeFile(t, path, strings.Join([]string{
		"1794470400\tlab:2024",
		"garbage",
		"x\tlab:2024\td",
		"1794470412\tlab:2024\tdemo-board\tconsole\traw\tfuture",
		"1794470413\t",
		"",
	}, "\n"))

	hist := readHistory(path)

	want := []histEntry{
		{at: time.Unix(1794470400, 0), host: "lab:2024"},
		{at: time.Unix(1794470412, 0), host: "lab:2024", device: "demo-board", line: "console", raw: true},
	}

	if len(hist.entries) != len(want) {
		t.Fatalf("read %v, want %v", hist.entries, want)
	}

	for i := range want {
		if !hist.entries[i].at.Equal(want[i].at) || hist.entries[i].String() != want[i].String() {
			t.Errorf("entry %d = %v, want %v", i, hist.entries[i], want[i])
		}
	}

	if empty := readHistory(filepath.Join(t.TempDir(), "none")); len(empty.entries) != 0 {
		t.Errorf("a missing file reads as %v", empty.entries)
	}

	if empty := readHistory(""); len(empty.entries) != 0 || empty.path != "" {
		t.Errorf("no path reads as %+v", empty)
	}
}

// An entry is appended to a file only the user can read, in a directory of
// the user's; past its limit the history is cut to its newest lines.
func TestWriteHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "dutctl", "history")
	entry := histEntry{at: time.Unix(1794470400, 0), host: "lab:2024", device: "dev", line: "say\thi"}

	for range 2 {
		if err := writeHistory(path, entry, nil); err != nil {
			t.Fatal(err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if want := "1794470400\tlab:2024\tdev\tsay hi\t\n"; string(data) != want+want {
		t.Errorf("file holds %q, want the line twice", data)
	}

	for file, mode := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700 | os.ModeDir} {
		info, err := os.Stat(file)
		if err != nil || info.Mode() != mode {
			t.Errorf("%s has mode %v (%v), want %v", file, info.Mode(), err, mode)
		}
	}

	var hist history

	for i := range historyMax {
		if hist.add(histEntry{at: time.Unix(int64(i), 0), host: "h"}) {
			t.Fatalf("cut at %d entries", i+1)
		}
	}

	if !hist.add(histEntry{at: time.Unix(historyMax, 0), host: "h"}) || len(hist.entries) != historyKeep {
		t.Fatalf("past the limit: %d entries", len(hist.entries))
	}

	if hist.entries[historyKeep-1].at.Unix() != historyMax {
		t.Errorf("the newest entry was cut")
	}

	if err := writeHistory(path, histEntry{}, hist.entries); err != nil {
		t.Fatal(err)
	}

	if got := readHistory(path); len(got.entries) != historyKeep {
		t.Errorf("the rewritten file holds %d entries", len(got.entries))
	}
}

// The menus see the history newest first, each thing once.
func TestHistoryProjections(t *testing.T) {
	line := func(secs int64, host, device, text string, raw bool) histEntry {
		return histEntry{at: time.Unix(secs, 0), host: host, device: device, line: text, raw: raw}
	}
	hist := history{entries: []histEntry{
		line(1, "lab:2024", "", "", false),
		line(2, "lab:2024", "board", "console", true),
		line(3, "bench:2024", "rpi", "", false),
		line(4, "lab:2024", "board", "power on", false),
		line(5, "lab:2024", "board", "console", false),
		line(6, "lab:2024", "agent", "", false),
	}}

	devices := hist.recentDevices(5)
	if len(devices) != 3 || devices[0].device != "agent" || devices[1].device != "board" || devices[2].device != "rpi" {
		t.Fatalf("recent devices %v", devices)
	}

	if devices[1].line != "console" || devices[1].raw || devices[0].line != "" {
		t.Errorf("a recent device's line: %+v", devices[1])
	}

	if got := hist.recentDevices(1); len(got) != 1 {
		t.Errorf("recent devices limited to 1: %v", got)
	}

	if hosts := hist.hosts(); len(hosts) != 2 || hosts[0].host != "lab:2024" || hosts[0].at.Unix() != 6 {
		t.Errorf("hosts %v", hosts)
	}

	if host, ok := hist.hostOf("rpi"); !ok || host != "bench:2024" {
		t.Errorf("hostOf(rpi) = %q, %v", host, ok)
	}
}

func TestAgo(t *testing.T) {
	now := time.Unix(1794470400, 0)

	for since, want := range map[time.Duration]string{
		10 * time.Second: "just now", 2 * time.Minute: "2 min ago", 3 * time.Hour: "3 h ago",
		30 * time.Hour: "yesterday", 72 * time.Hour: "3 days ago",
	} {
		if got := ago(now.Add(-since), now); got != want {
			t.Errorf("ago(%v) = %q, want %q", since, got, want)
		}
	}
}
