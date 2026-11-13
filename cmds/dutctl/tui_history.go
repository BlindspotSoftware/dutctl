// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// rawFlag marks a line whose run opened a raw console.
const rawFlag = "raw"

// History limits: past historyMax lines the file is rewritten with the newest
// historyKeep, so it never grows without bound.
const (
	historyMax  = 1000
	historyKeep = 500
)

// histEntry is one line of the TUI's history: a host visited, a device
// visited, or a line run on a device.
type histEntry struct {
	at     time.Time
	host   string // the completed dial address
	device string // empty for a host visit
	line   string // empty for a visit
	raw    bool   // the run opened a raw console
}

// history is what the TUI remembers between sessions, oldest first. It
// orders menus and fills lines; it is never a source of truth, every level is
// fetched from the agent before anything runs.
type history struct {
	path    string // empty when there is no place for it: the TUI works without
	entries []histEntry
}

// historyPath returns where the history lives: $XDG_STATE_HOME/dutctl/history,
// by default under $HOME/.local/state, or "" without either.
func historyPath() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(dir) {
		home := os.Getenv("HOME")
		if home == "" {
			return ""
		}

		dir = filepath.Join(home, ".local", "state")
	}

	return filepath.Join(dir, "dutctl", "history")
}

// readHistory reads the history at path. A missing or unreadable file is an
// empty history; a line it cannot parse is skipped.
func readHistory(path string) history {
	hist := history{path: path}
	if path == "" {
		return hist
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return hist
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if entry, ok := parseHistLine(line); ok {
			hist.entries = append(hist.entries, entry)
		}
	}

	return hist
}

// parseHistLine parses one line: unix seconds, host, device, line and flags,
// tab-separated. Columns after the fifth are ignored, so a later version may
// add some; the device and later ones may be missing.
func parseHistLine(text string) (histEntry, bool) {
	fields := strings.Split(text, "\t")

	const minFields = 2
	if len(fields) < minFields || fields[1] == "" {
		return histEntry{}, false
	}

	secs, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return histEntry{}, false
	}

	fields = append(fields, "", "", "") // the optional columns, empty when missing

	return histEntry{
		at:     time.Unix(secs, 0),
		host:   fields[1],
		device: fields[2],
		line:   fields[3],
		raw:    fields[4] == rawFlag,
	}, true
}

// String renders the entry as a line of the file, without the newline.
func (e histEntry) String() string {
	flags := ""
	if e.raw {
		flags = rawFlag
	}

	clean := strings.NewReplacer("\t", " ", "\n", " ", "\r", " ")

	return fmt.Sprintf("%d\t%s\t%s\t%s\t%s", e.at.Unix(),
		clean.Replace(e.host), clean.Replace(e.device), clean.Replace(e.line), flags)
}

// add records e in memory. It reports whether the history outgrew its limit
// and was cut to its newest lines: the file must then be rewritten as a whole.
func (h *history) add(e histEntry) bool {
	h.entries = append(h.entries, e)
	if len(h.entries) <= historyMax {
		return false
	}

	h.entries = append([]histEntry(nil), h.entries[len(h.entries)-historyKeep:]...)

	return true
}

// writeHistory appends entry to the file at path, or, with all set, replaces the
// file with all. The directory is private to the user, the file as well. Two
// TUIs may write at once: each line lands whole, the last rewrite wins.
func writeHistory(path string, entry histEntry, all []histEntry) error {
	const (
		dirMode  = 0o700
		fileMode = 0o600
	)

	err := os.MkdirAll(filepath.Dir(path), dirMode)
	if err != nil {
		return err
	}

	if all == nil {
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintln(file, entry)
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}

		return err
	}

	var text strings.Builder
	for _, entry := range all {
		fmt.Fprintln(&text, entry)
	}

	tmp := path + ".tmp"

	err = os.WriteFile(tmp, []byte(text.String()), fileMode)
	if err != nil {
		return err
	}

	return os.Rename(tmp, path)
}

// recentDevices returns the devices used most recently, newest first, one per
// host and device, each with the newest line run on it.
func (h *history) recentDevices(limit int) []histEntry {
	var out []histEntry

	index := map[[2]string]int{}

	for i := len(h.entries) - 1; i >= 0; i-- {
		entry := h.entries[i]
		if entry.device == "" {
			continue
		}

		key := [2]string{entry.host, entry.device}
		if at, ok := index[key]; ok {
			if out[at].line == "" && entry.line != "" {
				out[at].line, out[at].raw = entry.line, entry.raw
			}

			continue
		}

		if len(out) < limit {
			index[key] = len(out)
			out = append(out, entry)
		}
	}

	return out
}

// hosts returns the hosts visited, newest first, each once with the time of
// its newest line.
func (h *history) hosts() []histEntry {
	var out []histEntry

	seen := map[string]bool{}

	for i := len(h.entries) - 1; i >= 0; i-- {
		entry := h.entries[i]
		if seen[entry.host] {
			continue
		}

		seen[entry.host] = true

		out = append(out, histEntry{at: entry.at, host: entry.host})
	}

	return out
}

// hostOf returns the host device was used on most recently.
func (h *history) hostOf(device string) (string, bool) {
	for i := len(h.entries) - 1; i >= 0; i-- {
		if h.entries[i].device == device {
			return h.entries[i].host, true
		}
	}

	return "", false
}

// ago renders how long ago at was, coarsely: a menu row is no clock.
func ago(at, now time.Time) string {
	const day = 24 * time.Hour

	since := now.Sub(at)

	switch {
	case since < time.Minute:
		return "just now"
	case since < time.Hour:
		return fmt.Sprintf("%d min ago", int(since/time.Minute))
	case since < day:
		return fmt.Sprintf("%d h ago", int(since/time.Hour))
	case since < 2*day:
		return "yesterday"
	default:
		return fmt.Sprintf("%d days ago", int(since/day))
	}
}
