// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os"
	"testing"
)

func TestIsTTY(t *testing.T) {
	// A non-*os.File stream is never a terminal.
	if isTTY(&bytes.Buffer{}) {
		t.Error("bytes.Buffer should not be reported as a terminal")
	}

	// A regular file is not a terminal — this is the case that matters:
	// redirected output must get neither colour nor raw-mode housekeeping.
	file, err := os.CreateTemp(t.TempDir(), "tty")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if isTTY(file) {
		t.Error("a regular file should not be reported as a terminal")
	}

	// /dev/null is a character device, yet no terminal.
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()

	if isTTY(null) {
		t.Error("/dev/null should not be reported as a terminal")
	}
}
