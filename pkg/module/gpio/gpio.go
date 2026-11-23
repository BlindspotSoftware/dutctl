// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gpio provides modules that simulate buttons and switches and read
// digital inputs, using the GPIO pins of the Raspberry Pi. The pins used must be
// wired to the respective pads or connections on the DUT.
//
// For example, these modules can be used to pull down the reset line of the DUT
// or to read whether its power-good signal is set.
package gpio

import (
	"fmt"
)

// Pin is a raw BCM2835/BCM2711 GPIO pin number.
type Pin uint8

// maxPin is the highest GPIO number of the BCM2711 (Raspberry Pi 4); the
// BCM2835 family ends at 53. go-rpio turns the number into a register offset
// unchecked, so a larger one reaches registers that are not that pin's.
const maxPin Pin = 57

// validate reports a pin number that no supported chip has.
func (p Pin) validate() error {
	if p > maxPin {
		return fmt.Errorf("pin %d does not exist: BCM GPIO numbers go from 0 to %d", p, maxPin)
	}

	return nil
}

// State is the logic level of a GPIO pin, either [Low] or [High].
type State uint8

// Low and High are the logic levels of a GPIO [Pin].
const (
	Low  State = 0
	High State = 1
)

// pinLabel is "<label> (GPIO <pin>)", or "GPIO <pin>" without a label.
func pinLabel(label string, pin Pin) string {
	if label == "" {
		return fmt.Sprintf("GPIO %d", pin)
	}

	return fmt.Sprintf("%s (GPIO %d)", label, pin)
}

// gpio is an interface for different internal gpio access methods.
type gpio interface {
	Low(pin Pin) error
	High(pin Pin) error
	Toggle(pin Pin) error
	// Input sets pin to input mode with the given pull resistor.
	Input(pin Pin, p pull) error
	// Read sets pin to input mode and returns its level.
	Read(pin Pin) (State, error)
}

// pull is the internal resistor a GPIO input pin is connected through.
type pull uint8

const (
	pullOff  pull = iota // no resistor; the pin floats unless the DUT drives it
	pullUp               // the pin reads high unless the DUT drives it low
	pullDown             // the pin reads low unless the DUT drives it high
)

// backendParser is a function that returns a gpio backend for the given name.
type backendParser func(name string) gpio

// backendFromOption is the default [backendParser] function.
// Currently only "devmem" is supported; any unrecognized name falls back to "devmem".
func backendFromOption(name string) gpio {
	switch name {
	case "devmem":
		return &devmem{}
	default:
		return &devmem{}
	}
}
