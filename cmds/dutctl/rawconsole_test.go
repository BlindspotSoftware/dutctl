// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
)

// TestRawConsoleNeverArmsForNonFileStdin verifies that a non-*os.File stdin
// (e.g. a piped/scripted run) never switches to raw mode or prints the banner,
// and that arm/disarm are safe no-ops in that case.
func TestRawConsoleNeverArmsForNonFileStdin(t *testing.T) {
	var banner bytes.Buffer

	console := newRawConsole(&bytes.Buffer{}, &banner)

	console.arm()

	if console.isActive() {
		t.Error("isActive() = true for non-file stdin, want false")
	}

	if banner.Len() != 0 {
		t.Errorf("banner = %q, want none for non-file stdin", banner.String())
	}

	// disarm must not panic even though arm never engaged.
	console.disarm()
}

// TestRawConsoleArmIsIdempotent verifies that repeated arm calls (one per
// console message) do not panic and leave a consistent state. With a non-file
// stdin it stays inactive; the point is that calling arm many times is safe.
func TestRawConsoleArmIsIdempotent(t *testing.T) {
	console := newRawConsole(&bytes.Buffer{}, &bytes.Buffer{})

	for range 5 {
		console.arm()
	}

	if console.isActive() {
		t.Error("isActive() = true for non-file stdin, want false")
	}

	console.disarm()
	console.disarm() // double disarm must be safe
}
