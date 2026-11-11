// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package demo

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID:  "wrap-prompt",
		New: func() module.Module { return &WrapPrompt{} },
	})
}

// WrapPrompt wraps the line console of module.Session: it asks the user a
// question and answers the line the user enters. It shows how a module
// assembles lines from the chunks it reads, and that the last line of piped
// input may lack its newline.
type WrapPrompt struct{}

// Ensure implementing the Module interface.
var _ module.Module = &WrapPrompt{}

// Help describes what the module asks.
func (w *WrapPrompt) Help() string {
	return `Ask for your name in a line console and greet you.

ARGUMENTS:
	none

Your terminal edits the line, and Enter sends it. Ctrl-D at the start of the
line ends the input unanswered, and Ctrl-C ends the command.
`
}

// Init has nothing to set up.
func (w *WrapPrompt) Init(_ context.Context) error {
	return nil
}

// Deinit has nothing to release.
func (w *WrapPrompt) Deinit(_ context.Context) error {
	return nil
}

// Run asks for the user's name and greets the user by it. Input that ends
// before a name, or a session torn down meanwhile, ends the console with no
// greeting.
func (w *WrapPrompt) Run(_ context.Context, s module.Session, args ...string) error {
	if len(args) > 0 {
		return fmt.Errorf("expected no arguments, got %d", len(args))
	}

	con := s.OpenConsole(module.ConsoleOptions{Mode: module.ConsoleLine})
	s.Print("What is your name? ")

	// The bytes before io.EOF are a line too: piped input may not end in '\n'.
	line, err := bufio.NewReader(con.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}

	name := strings.TrimSpace(line)
	if name == "" {
		s.Println("\nNo name, no greeting.")

		return nil
	}

	s.Printf("Hello, %s!\n", name)

	return nil
}
