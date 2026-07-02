// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gpio provides two modules that simulate buttons and switches respectively, using the GPIO pins of
// the Raspberry Pi. Used pins of Raspberry Pi needs to be wired to respective pads/connections on the DUT.
//
// E.g. this module can be used to pull down the reset line of the DUT.
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
	module.Register(module.Record{
		ID: "gpio-switch",
		New: func() module.Module {
			return &Switch{backendParser: backendFromOption}
		},
	})
}

type Pin uint8
type State uint8

// gpio is an interface for different internal gpio access methods.
type gpio interface {
	Low(pin Pin) error
	High(pin Pin) error
	Toggle(pin Pin) error
}

// backendParser return a gpio backend based on the name.
type backendParser func(name string) gpio

// backendFromOption is the default [backendParser] function.
// Currently only "devmem" is supported.
// If the name is not recognized, the 'devmem' is used as a fallback.
func backendFromOption(name string) gpio {
	switch name {
	case "devmem":
		return &devmem{}
	default:
		return &devmem{}
	}
}

const (
	Low  State = 0
	High State = 1
)

const DefaultButtonPressDuration = 500 * time.Millisecond

// A Button simulates a button press by changing the state of a GPIO pin.
type Button struct {
	gpio
	backendParser

	Pin       Pin    // Raw BCM2835/BCM2711 pin number
	ActiveLow bool   // If set, the idle state is high, and low when pressed. Default is false
	Backend   string // For future use. Name of the backend to use. Default and fallback is "devmem"
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

// According to time.ParseDuration.
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
	help.WriteString(fmt.Sprintf("Default duration is %s.\n", DefaultButtonPressDuration))
	help.WriteString(description2Button)

	if b.ActiveLow {
		help.WriteString("The button is active low. Thus 'Idle' mean 'High', 'Pressed' means 'Low'\n")
	} else {
		help.WriteString("The button is active high. Thus 'Idle' mean 'Low', 'Pressed' means 'High'\n")
	}

	help.WriteString(fmt.Sprintf("The used GPIO pin is pin %d. (Raw BCM2835/BCM2711 pin number)\n", b.Pin))
	help.WriteString(description3Button)

	return help.String()
}

func (b *Button) Init(ctx context.Context) error {
	b.gpio = b.backendParser(b.Backend)

	log.FromContext(ctx).Debug(fmt.Sprintf("initializing pin %d to idle", b.Pin))

	if b.ActiveLow {
		// with active low, idle is high
		return b.High(b.Pin)
	}

	return b.Low(b.Pin)
}

func (b *Button) Deinit(_ context.Context) error {
	if b.gpio == nil {
		return nil
	}

	return b.Low(b.Pin)
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

	err = b.Toggle(b.Pin)
	if err != nil {
		return err
	}

	time.Sleep(duration)

	err = b.Toggle(b.Pin)
	if err != nil {
		return err
	}

	log.FromContext(ctx).Info(fmt.Sprintf("button press for %s (pin %d)", duration, b.Pin))
	s.Printf("Button pressed for %s\n", duration)

	return nil
}

type switchState string

const (
	on  switchState = "on"
	off switchState = "off"
)

// A Switch simulates an on/off switch by changing the state of a GPIO pin.
// By default, the switch is off and off means the pin is low.
type Switch struct {
	gpio
	backendParser

	// Raw BCM2835/BCM2711 pin number
	Pin Pin
	// Initial state of the switch: "on" or "off" (case insensitive). Default and fallback is "off".
	Initial string
	// If true, the switch is active low (switch on means gpio pin low). Default is false.
	ActiveLow bool
	// For future use. Name of the backend to use. Default is "devmem"
	Backend string

	state switchState
}

// Ensure implementing the Module interface.
var _ module.Module = &Switch{}

const abstractSwitch = `Simulate an on/off switch by changing the state of a GPIO pin
`
const usageSwitch = `
ARGUMENTS:
	[on|off|toggle]
`
const description1Switch = `
The on, off and toggle commands control the state of the switch.
If no argument is passed, the current state is printed.

`
const description2Switch = `
It is the users responsibility to ensure that the used GPIO pin is not also used by 
other modules or otherwise occupied by the system!
`

func (s *Switch) Help() string {
	help := strings.Builder{}
	help.WriteString(abstractSwitch)
	help.WriteString(usageSwitch)
	help.WriteString(description1Switch)

	if s.ActiveLow {
		help.WriteString("The switch is active low. Thus 'On' mean 'Low', 'Off' means 'High'\n")
	} else {
		help.WriteString("The switch is active high. Thus 'On' mean 'High', 'Off' means 'Low'\n")
	}

	help.WriteString(fmt.Sprintf("The used GPIO pin is pin %d. (Raw BCM2835/BCM2711 pin number)\n", s.Pin))
	help.WriteString(description2Switch)

	return help.String()
}

func (s *Switch) Init(ctx context.Context) error {
	s.gpio = s.backendParser(s.Backend)

	initial := strings.ToLower(s.Initial)
	if initial == "on" {
		s.state = on
		log.FromContext(ctx).Debug(fmt.Sprintf("initializing pin %d to on", s.Pin))

		return s.on()
	}

	s.state = off
	log.FromContext(ctx).Debug(fmt.Sprintf("initializing pin %d to off", s.Pin))

	return s.off()
}

func (s *Switch) Deinit(_ context.Context) error {
	if s.gpio == nil {
		return nil
	}

	return s.Low(s.Pin)
}

//nolint:cyclop,funlen // on/off/toggle branches each set state and log; long but linear and readable.
func (s *Switch) Run(ctx context.Context, sesh module.Session, args ...string) error {
	l := log.FromContext(ctx)

	if len(args) == 0 {
		sesh.Printf("Current state: %s\n", s.state)

		return nil
	}

	switch args[0] {
	case "on":
		err := s.on()
		if err != nil {
			return err
		}

		l.Info(fmt.Sprintf("switch on (pin %d)", s.Pin))

		if s.state == on {
			sesh.Print("Already on")
		} else {
			sesh.Print("Turned on")
		}

		s.state = on

		return nil
	case "off":
		err := s.off()
		if err != nil {
			return err
		}

		l.Info(fmt.Sprintf("switch off (pin %d)", s.Pin))

		if s.state == off {
			sesh.Print("Already off")
		} else {
			sesh.Print("Turned off")
		}

		s.state = off

		return nil
	case "toggle":
		err := s.Toggle(s.Pin)
		if err != nil {
			return err
		}

		if s.state == on {
			sesh.Print("Turned off")

			s.state = off
		} else {
			sesh.Print("Turned on")

			s.state = on
		}

		l.Info(fmt.Sprintf("switch %s (pin %d)", s.state, s.Pin))
	default:
		return fmt.Errorf("unknown argument: %s", args[0])
	}

	return nil
}

func (s *Switch) on() error {
	if s.ActiveLow {
		return s.Low(s.Pin)
	}

	return s.High(s.Pin)
}

func (s *Switch) off() error {
	if s.ActiveLow {
		return s.High(s.Pin)
	}

	return s.Low(s.Pin)
}
