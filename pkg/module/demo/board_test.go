// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package demo

import (
	"testing"

	"github.com/BlindspotSoftware/dutctl/pkg/module/demo/internal/board"
)

// freshBoard gives the test a board of its own, off and with the factory
// image, as demoBoard. The tests of this package share demoBoard, so none of
// them runs in parallel.
func freshBoard(t *testing.T) *board.Board {
	t.Helper()

	prev := demoBoard
	demoBoard = board.New()

	t.Cleanup(func() { demoBoard = prev })

	return demoBoard
}
