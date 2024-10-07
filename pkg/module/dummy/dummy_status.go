// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Dummy module implementation.
package dummy

import (
	"context"
	"fmt"
	"log"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Info{
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
	log.Println("dummy.Status module: Help called")

	return "This dummy module prints status information about itself and the environment."
}

func (d *Status) Init() error {
	log.Println("dummy.Status module: Init called")

	return nil
}

func (d *Status) Deinit() error {
	log.Println("dummy.Status module: Deinit called")

	return nil
}

func (d *Status) Run(_ context.Context, s module.Session, args ...string) error {
	log.Println("dummy.Status module: Run called")

	s.Print("Hello from dummy status module")

	str := fmt.Sprintf("Called with %d arguments", len(args))
	s.Print(str)

	for i, arg := range args {
		str := fmt.Sprintf("Arg %d: %s", i, arg)
		s.Print(str)
	}

	return nil
}
