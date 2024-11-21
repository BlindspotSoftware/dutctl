// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The gpio package provides two modules that simulate buttons and switches respectively, using the GPIO pins of
// the Raspberry Pi. Used pins of Raspberry Pi needs to be wired to respective pads/connections on the DUT.
//
// E.g. this module can be used to pull down the reset line of the DUT.
package gpio

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
		ID: "gpio-button",
		New: func() module.Module {
			return &Button{}
		},
	})
	module.Register(module.Record{
		ID: "gpio-switch",
		New: func() module.Module {
			return &Switch{}
		},
	})
}

type Pin uint8
type State uint8

const (
	Low  State = 0
	High State = 1
)

const DefaultButtonPressDuration = 500 * time.Millisecond

// A Button simulates a button press by changing the state of a GPIO pin.
type Button struct {
	Pin     Pin    // Raw BCM2835/BCM2711 pin number
	Idle    State  // State of the pin when not pressed. 0 for Low, 1 for High. Default is Low
	Backend string // For future use. Name of the backend to use. Default is "devmem"

	button
}

// button is an interface for a internal button backend.
type button interface {
	Init(pin Pin, idle State) error
	Press(duration time.Duration) error
	Deinit() error
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
	log.Println("gpio.Button module: Help called")

	help := strings.Builder{}
	help.WriteString(abstractButton)
	help.WriteString(usageButton)
	help.WriteString(description1Button)
	help.WriteString(fmt.Sprintf("Default duration is %s.\n", DefaultButtonPressDuration))
	help.WriteString(description2Button)

	var idleState string
	if b.Idle == Low {
		idleState = "Low"
	} else {
		idleState = "High"
	}

	help.WriteString(fmt.Sprintf("The idle state (if the not pressed) is %s.\n", idleState))
	help.WriteString(fmt.Sprintf("The used GPIO pin is pin %d. (Raw BCM2835/BCM2711 pin number)\n", b.Pin))
	help.WriteString(description3Button)

	return help.String()
}

func (b *Button) Init() error {
	log.Println("gpio.Button module: Init called")

	var backend button

	switch b.Backend {
	case "devmem":
		backend = &devmemButton{}
	default:
		backend = &devmemButton{}
	}

	err := backend.Init(b.Pin, b.Idle)
	if err != nil {
		return err
	}

	b.button = backend

	return nil
}

func (b *Button) Deinit() error {
	log.Println("gpio.Button module: Deinit called")

	return b.button.Deinit()
}

func (b *Button) Run(_ context.Context, s module.Session, args ...string) error {
	log.Println("gpio.Button module: Run called")

	var duration time.Duration

	if len(args) > 0 {
		d, err := time.ParseDuration(args[0])
		if err != nil {
			return err
		}

		duration = d
	} else {
		duration = DefaultButtonPressDuration
	}

	err := b.Press(duration)
	if err != nil {
		return err
	}

	s.Print(fmt.Sprintf("Button pressed for %s", duration))

	return nil
}

type stateStr string

const (
	on  stateStr = "on"
	off stateStr = "off"
)

// A Switch simulates an on/off switch by changing the state of a GPIO pin.
// By default, the switch is off and off means the pin is low.
type Switch struct {
	// Raw BCM2835/BCM2711 pin number
	Pin Pin
	// Initial state of the pin.  0 for On, 1 for Off. Default is Off.
	Initial State
	// If true, the switch is active low. Default is false.
	ActiveLow bool
	// For future use. Name of the backend to use. Default is "devmem"
	Backend string

	switcher
	state stateStr
}

// switcher is an interface for a internal button backend.
type switcher interface {
	Init(pin Pin, initial State, activeLow bool) error
	On() error
	Off() error
	Toggle() error
	Deinit() error
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
	log.Println("gpio.Switch module: Help called")

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

func (s *Switch) Init() error {
	log.Println("gpio.Switch module: Init called")

	var backend switcher

	switch s.Backend {
	case "devmem":
		backend = &devmemSwitch{}
	default:
		backend = &devmemSwitch{}
	}

	err := backend.Init(s.Pin, s.Initial, s.ActiveLow)
	if err != nil {
		return err
	}

	s.switcher = backend
	if s.Initial == Low {
		s.state = off
	} else {
		s.state = on
	}

	return nil
}

func (s *Switch) Deinit() error {
	log.Println("gpio.Switch module: Deinit called")

	return s.switcher.Deinit()
}

//nolint:cyclop
func (s *Switch) Run(_ context.Context, sesh module.Session, args ...string) error {
	log.Println("gpio.Switch module: Run called")

	if len(args) == 0 {
		sesh.Print(fmt.Sprintf("Current state: %s", s.state))

		return nil
	}

	switch args[0] {
	case "on":
		err := s.On()
		if err != nil {
			return err
		}

		if s.state == on {
			sesh.Print("Already on")
		} else {
			sesh.Print("Turned on")
		}

		s.state = on

		return nil
	case "off":
		err := s.Off()
		if err != nil {
			return err
		}

		if s.state == off {
			sesh.Print("Already off")
		} else {
			sesh.Print("Turned off")
		}

		s.state = off

		return nil
	case "toggle":
		err := s.Toggle()
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
	default:
		return fmt.Errorf("unknown argument: %s", args[0])
	}

	return nil
}
