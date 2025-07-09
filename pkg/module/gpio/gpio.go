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
	Pin       Pin    // Raw BCM2835/BCM2711 pin number
	ActiveLow bool   // If set, the idle state is high, and low when pressed. Default is false
	Backend   string // For future use. Name of the backend to use. Default and fallback is "devmem"

	backendParser
	gpio
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

	if b.ActiveLow {
		help.WriteString("The button is active low. Thus 'Idle' mean 'High', 'Pressed' means 'Low'\n")
	} else {
		help.WriteString("The button is active high. Thus 'Idle' mean 'Low', 'Pressed' means 'High'\n")
	}

	help.WriteString(fmt.Sprintf("The used GPIO pin is pin %d. (Raw BCM2835/BCM2711 pin number)\n", b.Pin))
	help.WriteString(description3Button)

	return help.String()
}

func (b *Button) Init() error {
	log.Println("gpio.Button module: Init called")

	b.gpio = b.backendParser(b.Backend)

	if b.ActiveLow {
		// with active low, idle is high
		return b.gpio.High(b.Pin)
	}

	return b.gpio.Low(b.Pin)
}

func (b *Button) Deinit() error {
	log.Println("gpio.Button module: Deinit called")

	return b.gpio.Low(b.Pin)
}

func (b *Button) Run(_ context.Context, s module.Session, args ...string) error {
	log.Println("gpio.Button module: Run called")

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

	if err := b.gpio.Toggle(b.Pin); err != nil {
		return err
	}

	time.Sleep(duration)

	if err := b.gpio.Toggle(b.Pin); err != nil {
		return err
	}

	s.Print(fmt.Sprintf("Button pressed for %s", duration))

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
	// Raw BCM2835/BCM2711 pin number
	Pin Pin
	// Initial state of the switch: "on" or "off" (case insensitive). Default and fallback is "off".
	Initial string
	// If true, the switch is active low (switch on means gpio pin low). Default is false.
	ActiveLow bool
	// For future use. Name of the backend to use. Default is "devmem"
	Backend string

	gpio
	backendParser
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

	s.gpio = s.backendParser(s.Backend)

	initial := strings.ToLower(s.Initial)
	if initial == "on" {
		s.state = on

		return s.on()
	}

	return s.off()
}

func (s *Switch) Deinit() error {
	log.Println("gpio.Switch module: Deinit called")

	return s.gpio.Low(s.Pin)
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
		err := s.on()
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
		err := s.off()
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
		err := s.gpio.Toggle(s.Pin)
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

func (s *Switch) on() error {
	if s.ActiveLow {
		return s.gpio.Low(s.Pin)
	}

	return s.gpio.High(s.Pin)
}

func (s *Switch) off() error {
	if s.ActiveLow {
		return s.gpio.High(s.Pin)
	}

	return s.gpio.Low(s.Pin)
}
