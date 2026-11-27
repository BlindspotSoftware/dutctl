// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package module_test

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// Greet is a module with one option: it greets each name that a command
// passes it. The agent's configuration sets the option Greeting in the with
// map of a step, as greeting: Hi.
type Greet struct {
	Greeting string
}

// Help describes the arguments and the greeting in effect.
func (g *Greet) Help() string {
	return fmt.Sprintf("Greet each name with %q.\n\nARGUMENTS:\n\tname...\n", g.Greeting)
}

// Init applies the default of the option.
func (g *Greet) Init(_ context.Context) error {
	if g.Greeting == "" {
		g.Greeting = "Hello"
	}

	return nil
}

// Deinit has nothing to release.
func (g *Greet) Deinit(_ context.Context) error {
	return nil
}

// Run greets each name it gets.
func (g *Greet) Run(_ context.Context, s module.Session, args ...string) error {
	if len(args) == 0 {
		return errors.New("expected at least one name")
	}

	for _, name := range args {
		s.Printf("%s, %s!\n", g.Greeting, name)
	}

	return nil
}

//nolint:gochecknoinits // a module registers itself from init, as the module packages do
func init() {
	module.Register(module.Record{
		ID:  "greet",
		New: func() module.Module { return &Greet{} },
	})
}

// A module package registers its modules from its init function. dutagent
// makes an instance of each module that its configuration names by ID, sets
// the options and calls Init once at its start.
func Example() {
	m, err := module.New("greet")
	if err != nil {
		fmt.Println(err)

		return
	}

	if err := m.Init(context.Background()); err != nil {
		fmt.Println(err)

		return
	}

	fmt.Println(strings.SplitN(m.Help(), "\n", 2)[0])
	// Output: Greet each name with "Hello".
}
