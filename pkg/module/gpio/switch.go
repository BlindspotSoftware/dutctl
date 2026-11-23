// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gpio

import (
	"context"
	"fmt"
	"strings"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID: "gpio-switch",
		New: func() module.Module {
			return &Switch{backendParser: backendFromOption}
		},
	})
}

type switchState string

const (
	on  switchState = "on"
	off switchState = "off"
)

// A Switch simulates an on/off switch by changing the state of a GPIO pin.
// By default, the switch is off and off means the pin is low.
type Switch struct {
	backendParser

	gpio gpio // pin access, selected in Init
	// Raw BCM2835/BCM2711 pin number
	Pin Pin
	// Initial state of the switch: "on" or "off" (case insensitive). Default and fallback is "off".
	Initial string
	// If true, the switch is active low (switch on means gpio pin low). Default is false.
	ActiveLow bool
	// For future use. Name of the backend to use. Default is "devmem"
	Backend string
	// Label is an optional string tag to describe the switches purpose or connected item.
	// E.g "Host reset line" or "Green status LED".
	Label string

	state switchState
}

// label returns a description of s using as much information as available. Fallback is just the pin number.
func (s *Switch) label() string {
	return pinLabel(s.Label, s.Pin)
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

	fmt.Fprintf(&help, "The used GPIO pin is pin %d. (Raw BCM2835/BCM2711 pin number)\n", s.Pin)
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

// Deinit leaves the switch off.
func (s *Switch) Deinit(_ context.Context) error {
	if s.gpio == nil {
		return nil
	}

	return s.off()
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
			sesh.Printf("%s: already on\n", s.label())
		} else {
			sesh.Printf("%s: turned on\n", s.label())
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
			sesh.Printf("%s: already off\n", s.label())
		} else {
			sesh.Printf("%s: turned off\n", s.label())
		}

		s.state = off

		return nil
	case "toggle":
		err := s.gpio.Toggle(s.Pin)
		if err != nil {
			return err
		}

		if s.state == on {
			sesh.Printf("%s: turned off\n", s.label())

			s.state = off
		} else {
			sesh.Printf("%s: turned on\n", s.label())

			s.state = on
		}

		l.Info(fmt.Sprintf("switch %s (pin %d)", s.state, s.Pin))
	default:
		return fmt.Errorf("unknown argument: %s", args[0])
	}

	return nil
}

func (s *Switch) on() error {
	return drive(s.gpio, s.Pin, activeLevel(s.ActiveLow))
}

func (s *Switch) off() error {
	return drive(s.gpio, s.Pin, activeLevel(!s.ActiveLow))
}
