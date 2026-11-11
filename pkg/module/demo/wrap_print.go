// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package demo

import (
	"context"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID:  "wrap-print",
		New: func() module.Module { return &WrapPrint{} },
	})
}

// WrapPrint wraps the Print family of module.Session: it prints its
// arguments as one line. It shows what a command hands to a module, fixed,
// passed through or templated arguments, and lets a step of a command say
// what the command does.
type WrapPrint struct{}

// Ensure implementing the Module interface.
var _ module.Module = &WrapPrint{}

// Help describes the arguments.
func (w *WrapPrint) Help() string {
	return `Print the arguments, separated by spaces, as one line.

ARGUMENTS:
	[text...]
`
}

// Init has nothing to set up.
func (w *WrapPrint) Init(_ context.Context) error {
	return nil
}

// Deinit has nothing to release.
func (w *WrapPrint) Deinit(_ context.Context) error {
	return nil
}

// Run prints args.
func (w *WrapPrint) Run(_ context.Context, s module.Session, args ...string) error {
	s.Println(strings.Join(args, " "))

	return nil
}
