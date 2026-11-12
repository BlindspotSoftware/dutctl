// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && !race

package integration

// raceEnabled tells that the tests run with the race detector, so the
// binaries under test are built with it too.
const raceEnabled = false
