// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package demo provides modules that need no hardware, so that dutctl can be
// tried, and its features shown and tested, on any machine. They come in two
// kinds:
//
//   - The wrap modules each wrap one feature of module.Session and do nothing
//     else: printing, a line console, a raw console, receiving a file,
//     sending a file, failing. They show what a module can do, one feature
//     at a time.
//   - The demo modules drive a simulated board, each one part of it, as real
//     modules drive the parts of the wiring to a real device: its power, its
//     serial console and its flash. They show how to build a module for a
//     device, and how a configuration turns such modules into commands.
package demo

import "github.com/BlindspotSoftware/dutctl/pkg/module/demo/internal/board"

// demoBoard is the simulated board the demo modules drive. There is one per
// agent: the modules of all commands share it, as the commands of a real
// device share its hardware, so a power-on by one command boots the board on
// the console that another command opened.
//
//nolint:gochecknoglobals // the one simulated board stands for hardware that all commands share
var demoBoard = board.New()
