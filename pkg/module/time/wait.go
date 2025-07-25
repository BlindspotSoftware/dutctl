// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID:  "time-wait",
		New: func() module.Module { return &Wait{} },
	})
}

// DefaultDuration is the default duration for the Wait module if none is set via configuration
// or provided via the command line.
const DefaultDuration = "1s"

// Wait is a module that waits for a specified amount of time.
type Wait struct {
	Duration string // Duration is the amount of time to wait. Must conform to time.ParseDuration.
	duration time.Duration
}

// Ensure implementing the Module interface.
var _ module.Module = &Wait{}

const abstract = `Wait for a certain amount of time
`
const usage = `
SYNOPSIS:
	time-wait [duration]

`
const description1 = `
Pass a duration as the first argument. If no duration is passed, the configured duration is used.
`

// According to time.ParseDuration.
const description2 = `
A duration string is a possibly signed sequence of decimal numbers, each with optional fraction
and a unit suffix, such as "300ms", "-1.5h" or "2h45m".
Valid time units are "ns", "us" (or "µs"), "ms", "s", "m", "h".`

func (w *Wait) Help() string {
	log.Println("time.Wait module: Help called")

	help := strings.Builder{}
	help.WriteString(abstract)
	help.WriteString(usage)
	help.WriteString(description1)
	help.WriteString(fmt.Sprintf("Configured duration is %s\n", DefaultDuration))
	help.WriteString(description2)

	return help.String()
}

func (w *Wait) Init() error {
	log.Println("time.Wait module: Init called")

	if w.Duration == "" {
		w.Duration = DefaultDuration
	}

	d, err := time.ParseDuration(w.Duration)
	if err != nil {
		return err
	}

	w.duration = d

	return nil
}

func (w *Wait) Deinit() error {
	log.Println("time.Wait module: Deinit called")

	return nil
}

func (w *Wait) Run(_ context.Context, s module.Session, args ...string) error {
	log.Println("time.Wait module: Run called")

	var duration time.Duration

	// Override default duration or configured duration with value passed via cmd line.
	if len(args) > 0 {
		d, err := time.ParseDuration(args[0])
		if err != nil {
			return err
		}

		duration = d
	} else {
		duration = w.duration
	}

	str := fmt.Sprintf("Waiting for %s", duration)
	s.Print(str)

	time.Sleep(duration)

	return nil
}
