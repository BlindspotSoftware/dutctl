// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gpio

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID: "gpio-button",
		New: func() module.Module {
			return &Button{backendParser: backendFromOption}
		},
	})
}

// DefaultButtonPressDuration is the press duration used when Button.Run
// is invoked without a duration argument.
const DefaultButtonPressDuration = 500 * time.Millisecond

// A Button simulates a button press by changing the state of a GPIO pin.
type Button struct {
	backendParser

	gpio      gpio   // pin access, selected in Init
	Pin       Pin    // Raw BCM2835/BCM2711 pin number
	ActiveLow bool   // If set, the idle state is high, and low when pressed. Default is false
	Backend   string // For future use. Name of the backend to use. Default and fallback is "devmem"
	Label     string // Optional string tag to describe the button's purpose or connected item, e.g. "Host reset line"
}

// label returns a description of b using as much information as available. Fallback is just the pin number.
func (b *Button) label() string {
	return pinLabel(b.Label, b.Pin)
}

// Ensure implementing the Module interface.
var _ module.Module = &Button{}

const abstractButton = `Simulate a button press by changing the state of a GPIO pin
`
const usageButton = `
ARGUMENTS:
	[duration]

`
const description1Button = `
The duration controls the time the button is pressed. If no duration is passed, a default is used.
`

// description2Button restates the duration-string syntax from time.ParseDuration.
const description2Button = `
A duration string is a possibly signed sequence of decimal numbers, each with optional fraction
and a unit suffix, such as "300ms", "-1.5h" or "2h45m".
Valid time units are "ns", "us" (or "µs"), "ms", "s", "m", "h".

`
const description3Button = `
It is the users responsibility to ensure that the used GPIO pin is not also used by 
other modules or otherwise occupied by the system!
`

func (b *Button) Help() string {
	help := strings.Builder{}
	help.WriteString(abstractButton)
	help.WriteString(usageButton)
	help.WriteString(description1Button)
	fmt.Fprintf(&help, "Default duration is %s.\n", DefaultButtonPressDuration)
	help.WriteString(description2Button)

	if b.ActiveLow {
		help.WriteString("The button is active low. Thus 'Idle' mean 'High', 'Pressed' means 'Low'\n")
	} else {
		help.WriteString("The button is active high. Thus 'Idle' mean 'Low', 'Pressed' means 'High'\n")
	}

	fmt.Fprintf(&help, "The used GPIO pin is pin %d. (Raw BCM2835/BCM2711 pin number)\n", b.Pin)
	help.WriteString(description3Button)

	return help.String()
}

func (b *Button) Init(ctx context.Context) error {
	b.gpio = b.backendParser(b.Backend)

	log.FromContext(ctx).Debug(fmt.Sprintf("initializing pin %d to idle", b.Pin))

	return b.idle()
}

// Deinit leaves the button released.
func (b *Button) Deinit(_ context.Context) error {
	if b.gpio == nil {
		return nil
	}

	return b.idle()
}

func (b *Button) Run(ctx context.Context, s module.Session, args ...string) error {
	var (
		duration time.Duration
		err      error
	)

	if len(args) > 0 {
		duration, err = time.ParseDuration(args[0])
		if err != nil {
			return err
		}
	} else {
		duration = DefaultButtonPressDuration
	}

	err = b.gpio.Toggle(b.Pin)
	if err != nil {
		return err
	}

	// Hold for the press duration but honor cancellation/deadline. Release the
	// button afterwards regardless, so an interrupted press does not leave the pin
	// held.
	waitErr := sleepCtx(ctx, duration)

	err = b.gpio.Toggle(b.Pin)
	if err != nil {
		return err
	}

	if waitErr != nil {
		return waitErr
	}

	log.FromContext(ctx).Info(fmt.Sprintf("button press for %s (pin %d)", duration, b.Pin))
	s.Printf("%s: pressed for %s\n", b.label(), duration)

	return nil
}

// idle drives the pin to the level of a released button: high if the button is
// active low, low otherwise.
func (b *Button) idle() error {
	return drive(b.gpio, b.Pin, activeLevel(!b.ActiveLow))
}

// sleepCtx pauses for d, returning ctx.Err() if ctx is done first. A non-positive
// d returns nil immediately.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
