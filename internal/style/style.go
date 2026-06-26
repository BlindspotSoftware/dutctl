// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package style holds the shared visual vocabulary for the client's
// human-readable output.
package style

// ANSI color codes.
const (
	Reset  = "\033[0m"
	Gray   = "\033[90m" // muted: context / metadata
	Cyan   = "\033[36m" // info: file transfer
	Yellow = "\033[33m" // warning
	Red    = "\033[31m" // error
	Green  = "\033[32m" // success
)

// Markers prefix a line of certain client output so it is visually
// distinct from payload, which carries no marker.
const (
	MarkerContext  = "#" // metadata / context
	MarkerSent     = "↑" // file sent to the agent
	MarkerReceived = "↓" // file received from the agent
	MarkerSuccess  = "✓" // a successful action
	MarkerWarning  = "⚠" // a warning
	MarkerError    = "✗" // an error
)

// Colorize wraps s in code (followed by Reset) when enabled is true and code is
// non-empty; otherwise it returns s unchanged. This is the single place that
// decides whether color is emitted.
func Colorize(enabled bool, code, s string) string {
	if !enabled || code == "" {
		return s
	}

	return code + s + Reset
}
