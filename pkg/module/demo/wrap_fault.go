// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package demo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID:  "wrap-fault",
		New: func() module.Module { return &WrapFault{} },
	})
}

// WrapFault wraps the two ways a module's Run fails: it returns an error, as
// a module does when its device fails, or it panics, as a module with a bug
// might. It shows what dutctl reports then, and that the agent recovers a
// module's panic and goes on serving.
type WrapFault struct{}

// Ensure implementing the Module interface.
var _ module.Module = &WrapFault{}

// Help describes the two faults.
func (w *WrapFault) Help() string {
	return `Fail on purpose, to see what dutctl reports.

ARGUMENTS:
	error|panic [message...]

  error  returns an error, as a module does when its device fails
  panic  panics, as a module with a bug might; the agent recovers it
`
}

// Init has nothing to set up.
func (w *WrapFault) Init(_ context.Context) error {
	return nil
}

// Deinit has nothing to release.
func (w *WrapFault) Deinit(_ context.Context) error {
	return nil
}

// Run fails as args say, with the rest of args as the message.
func (w *WrapFault) Run(_ context.Context, _ module.Session, args ...string) error {
	if len(args) == 0 {
		return errors.New("expected error or panic")
	}

	msg := "the wrap-fault module failed as asked"
	if len(args) > 1 {
		msg = strings.Join(args[1:], " ")
	}

	switch args[0] {
	case "error":
		return errors.New(msg)
	case "panic":
		panic(msg)
	default:
		return fmt.Errorf("unknown fault %q, want error or panic", args[0])
	}
}
