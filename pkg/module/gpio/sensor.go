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

// pull is the internal resistor a GPIO input pin is connected through.
type pull uint8

const (
	pullOff  pull = iota // no resistor; the pin floats unless the DUT drives it
	pullUp               // the pin reads high unless the DUT drives it low
	pullDown             // the pin reads low unless the DUT drives it high
)

// pullOffName is the Pull option that leaves the pin without a resistor, and
// what an unset Pull means.
const pullOffName = "off"

// parsePull parses the Pull option of a [Sensor]. The empty string means "off".
func parsePull(s string) (pull, error) {
	switch strings.ToLower(s) {
	case "", pullOffName:
		return pullOff, nil
	case "up":
		return pullUp, nil
	case "down":
		return pullDown, nil
	default:
		return pullOff, fmt.Errorf("unknown pull %q: want up, down or off", s)
	}
}

// A Sensor reads the level of a GPIO pin that the DUT drives, such as a
// power-good signal or a status LED. The module never drives the pin.
type Sensor struct {
	backendParser

	gpio gpio // pin access, selected in Init
	// Raw BCM2835/BCM2711 pin number
	Pin Pin
	// If true, the signal is active low: a low pin reads as active. Default is false.
	ActiveLow bool
	// Internal resistor of the pin: "up", "down" or "off" (case insensitive). Default is "off".
	Pull string
	// For future use. Name of the backend to use. Default is "devmem"
	Backend string
	// Label is an optional string tag to describe the sensor's purpose or connected item.
	// E.g "Power good" or "Status LED".
	Label string
}

// label returns a description of sn using as much information as available. Fallback is just the pin number.
func (sn *Sensor) label() string {
	return pinLabel(sn.Label, sn.Pin)
}

// Ensure implementing the Module interface.
var _ module.Module = &Sensor{}

const abstractSensor = `Read a signal the DUT drives on a GPIO pin
`
const usageSensor = `
ARGUMENTS:
	none

`
const description1Sensor = `
Prints whether the signal is active or inactive, followed by the pin's level.
The pin is only read, never driven.

`
const description2Sensor = `
It is the users responsibility to ensure that the used GPIO pin is not also used by
other modules or otherwise occupied by the system!
`

func (sn *Sensor) Help() string {
	help := strings.Builder{}
	help.WriteString(abstractSensor)
	help.WriteString(usageSensor)
	help.WriteString(description1Sensor)

	if sn.ActiveLow {
		help.WriteString("The signal is active low. Thus 'Active' means 'Low', 'Inactive' means 'High'\n")
	} else {
		help.WriteString("The signal is active high. Thus 'Active' means 'High', 'Inactive' means 'Low'\n")
	}

	fmt.Fprintf(&help, "The used GPIO pin is pin %d. (Raw BCM2835/BCM2711 pin number)\n", sn.Pin)

	pullName := strings.ToLower(sn.Pull)
	if pullName == "" {
		pullName = pullOffName
	}

	fmt.Fprintf(&help, "The internal pull resistor is %s.\n", pullName)
	help.WriteString(description2Sensor)

	return help.String()
}

func (sn *Sensor) Init(ctx context.Context) error {
	err := sn.Pin.validate()
	if err != nil {
		return err
	}

	pull, err := parsePull(sn.Pull)
	if err != nil {
		return err
	}

	sn.gpio = sn.backendParser(sn.Backend)

	log.FromContext(ctx).Debug(fmt.Sprintf("initializing pin %d as input", sn.Pin))

	return sn.gpio.Input(sn.Pin, pull)
}

// Deinit leaves the pin alone: an input pin drives nothing, and driving it now
// could fight the DUT's signal.
func (sn *Sensor) Deinit(_ context.Context) error {
	return nil
}

func (sn *Sensor) Run(_ context.Context, sesh module.Session, args ...string) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected argument: %s (gpio-sensor takes no arguments)", args[0])
	}

	level, err := sn.gpio.Read(sn.Pin)
	if err != nil {
		return err
	}

	active := level == High
	if sn.ActiveLow {
		active = !active
	}

	activity, levelName := "inactive", "low"
	if active {
		activity = "active"
	}

	if level == High {
		levelName = "high"
	}

	sesh.Printf("%s: %s (%s)\n", sn.label(), activity, levelName)

	return nil
}
