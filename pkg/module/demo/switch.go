// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package demo

import (
	"context"
	"fmt"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
	"github.com/BlindspotSoftware/dutctl/pkg/module/demo/internal/board"
)

func init() {
	module.Register(module.Record{
		ID:  "demo-switch",
		New: func() module.Module { return &Switch{} },
	})
}

// defaultCycle is how long a cycle keeps the line off when Cycle is unset.
const defaultCycle = time.Second

// Switch switches a line of the demo board on and off, as the gpio-switch
// module switches a GPIO pin wired to a real board. It knows the line it is
// wired to, not what the line is for: the configuration gives each use of it
// a meaning. It shows an option that Init checks, the Print family of
// module.Session, a module that returns as soon as its context is done, and
// the error a module returns for an argument it does not know.
type Switch struct {
	// Line is the board's line the switch is wired to: power or
	// write-protect.
	Line string
	// Cycle is how long a cycle keeps the line off, as a Go duration such as
	// 500ms; 1s when unset.
	Cycle string
	// line is Line, checked by Init.
	line board.Line
	// cycle is Cycle, parsed by Init.
	cycle time.Duration
}

// Ensure implementing the Module interface.
var _ module.Module = &Switch{}

// Help describes the arguments, the line and the configured cycle.
func (sw *Switch) Help() string {
	return fmt.Sprintf(`Switch the %s line of the simulated demo board, or show it.

ARGUMENTS:
	[on|off|toggle|cycle|status]

  on      switches the line on
  off     switches the line off
  toggle  switches the line to its other state
  cycle   switches the line off and, %s later, on again
  status  shows whether the line is on; the default

The power line boots the board when it goes on, which its console shows. The
write-protect line keeps the flash from being written while it is on. Ctrl-C
during a cycle aborts it and leaves the line off.
`, sw.line, sw.cycle)
}

// Init checks Line and parses Cycle. A switch wired to no line of the board,
// or a cycle that does not parse or is not positive, makes the configuration
// invalid.
func (sw *Switch) Init(_ context.Context) error {
	switch line := board.Line(sw.Line); line {
	case board.Power, board.WriteProtect:
		sw.line = line
	default:
		return fmt.Errorf("line: %q is no line of the board, want %s or %s", sw.Line, board.Power, board.WriteProtect)
	}

	sw.cycle = defaultCycle

	if sw.Cycle == "" {
		return nil
	}

	cycle, err := time.ParseDuration(sw.Cycle)
	if err != nil {
		return fmt.Errorf("cycle: %w", err)
	}

	if cycle <= 0 {
		return fmt.Errorf("cycle: %s is not positive", sw.Cycle)
	}

	sw.cycle = cycle

	return nil
}

// Deinit has nothing to release.
func (sw *Switch) Deinit(_ context.Context) error {
	return nil
}

// Run carries out the action in args, status if there is none. A cycle waits
// on ctx, so it ends as soon as the command is cancelled, with the line left
// off.
func (sw *Switch) Run(ctx context.Context, s module.Session, args ...string) error {
	if len(args) > 1 {
		return fmt.Errorf("expected at most one argument, got %d", len(args))
	}

	action := "status"
	if len(args) == 1 {
		action = args[0]
	}

	switch action {
	case "status":
		s.Printf("The %s line is %s.\n", sw.line, onOff(demoBoard.On(sw.line)))
	case "on":
		sw.set(s, true)
	case "off":
		sw.set(s, false)
	case "toggle":
		sw.set(s, !demoBoard.On(sw.line))
	case "cycle":
		return sw.runCycle(ctx, s)
	default:
		return fmt.Errorf("unknown action %q, want on, off, toggle, cycle or status", action)
	}

	return nil
}

// runCycle switches the line off and, after the configured time, on again,
// unless ctx is done before.
func (sw *Switch) runCycle(ctx context.Context, s module.Session) error {
	sw.set(s, false)
	s.Printf("Switching it on again in %s.\n", sw.cycle)

	timer := time.NewTimer(sw.cycle)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("cycle aborted, the %s line stays off: %w", sw.line, ctx.Err())
	case <-timer.C:
	}

	sw.set(s, true)

	return nil
}

// set switches the line and says what happened.
func (sw *Switch) set(s module.Session, on bool) {
	if demoBoard.Switch(sw.line, on) {
		s.Printf("The %s line is now %s.\n", sw.line, onOff(on))

		return
	}

	s.Printf("The %s line is already %s.\n", sw.line, onOff(on))
}

// onOff names the state of a line.
func onOff(on bool) string {
	if on {
		return "on"
	}

	return "off"
}
