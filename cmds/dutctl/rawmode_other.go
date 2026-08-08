// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !linux && !darwin

package main

// setRawInput is a no-op on platforms without termios support (e.g. Windows).
// Input stays line-buffered; the interactive serial experience is degraded but
// the client still builds and runs.
func setRawInput(_ int) func() {
	return nil
}
