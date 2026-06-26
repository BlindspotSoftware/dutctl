// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"io"
	"os"
)

// isTerminal reports whether w is connected to an interactive terminal (TTY).
//
// Caveat of the stdlib: character devices such as /dev/null also
// report true. That is harmless here (nothing reads color written to
// /dev/null), while pipes and regular files — the cases that matter — correctly
// report false.
func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}

	info, err := file.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}
