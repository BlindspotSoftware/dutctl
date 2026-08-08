// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
)

// TestRawConsoleNeverArmsForNonFileStdin verifies that a non-*os.File stdin
// (e.g. a piped/scripted run) never switches to raw mode, regardless of the
// interactive hint, and that arm/disarm are safe no-ops in that case.
func TestRawConsoleNeverArmsForNonFileStdin(t *testing.T) {
	console := newRawConsole(&bytes.Buffer{}, true)

	console.arm()

	if console.isActive() {
		t.Error("isActive() = true for non-file stdin, want false")
	}

	// disarm must not panic even though arm never engaged.
	console.disarm()
}

// TestRawConsoleNeverArmsForScriptedInvocation verifies that when the command
// was invoked with arguments (interactive=false), raw mode is never armed even
// though the agent streams console output (modelled here by calling arm).
func TestRawConsoleNeverArmsForScriptedInvocation(t *testing.T) {
	// interactive=false models a serial expect/send sequence run.
	console := newRawConsole(&bytes.Buffer{}, false)

	console.arm()

	if console.isActive() {
		t.Error("isActive() = true for a scripted (argument-bearing) invocation, want false")
	}

	console.disarm()
}

// TestRawConsoleArmIsIdempotent verifies that repeated arm calls (one per
// console message) do not panic and leave a consistent state. With a non-file
// stdin it stays inactive; the point is that calling arm many times is safe.
func TestRawConsoleArmIsIdempotent(t *testing.T) {
	console := newRawConsole(&bytes.Buffer{}, true)

	for range 5 {
		console.arm()
	}

	if console.isActive() {
		t.Error("isActive() = true for non-file stdin, want false")
	}

	console.disarm()
	console.disarm() // double disarm must be safe
}
