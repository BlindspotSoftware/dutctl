// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os"
	"testing"
)

func TestIsTerminal(t *testing.T) {
	// A non-*os.File writer is never a terminal.
	if isTerminal(&bytes.Buffer{}) {
		t.Error("bytes.Buffer should not be reported as a terminal")
	}

	// A regular file is not a character device, so it is not a terminal — this
	// is the case that matters: redirected output must not get color.
	file, err := os.CreateTemp(t.TempDir(), "tty")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if isTerminal(file) {
		t.Error("a regular file should not be reported as a terminal")
	}
}
