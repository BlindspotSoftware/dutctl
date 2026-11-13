// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !linux && !darwin

package main

// discardInput keeps the keys typed during a run: dropping them is not
// implemented here, so one may act on the prompt.
func discardInput(int) {}
