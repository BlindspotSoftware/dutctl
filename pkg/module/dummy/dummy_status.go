// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dummy

import (
	"context"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID:  "dummy-status",
		New: func() module.Module { return &Status{} },
	})
}

// Status prints status information about itself and the environment.
// It demonstrates the use of the Print method of module.Session to send messages to the client.
type Status struct{}

// Ensure implementing the Module interface.
var _ module.Module = &Status{}

func (d *Status) Help() string {
	return "This dummy module prints status information about itself and the environment."
}

func (d *Status) Init(_ context.Context) error {
	return nil
}

func (d *Status) Deinit(_ context.Context) error {
	return nil
}

func (d *Status) Run(_ context.Context, s module.Session, args ...string) error {
	s.Println("Hello from dummy status module")
	s.Printf("Called with %d arguments\n", len(args))

	for i, arg := range args {
		s.Printf("Arg %d: %s\n", i, arg)
	}

	return nil
}
