// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gpio

import (
	"fmt"

	"github.com/stianeikeland/go-rpio/v4"
)

type devmem struct{}

var _ gpio = &devmem{}

func (d *devmem) Low(p Pin) error {
	return memmapDo(func() {
		pin := rpio.Pin(p)
		pin.Output()
		pin.Low()
	})
}

func (d *devmem) High(p Pin) error {
	return memmapDo(func() {
		pin := rpio.Pin(p)
		pin.Output()
		pin.High()
	})
}

func (d *devmem) Toggle(pin Pin) error {
	return memmapDo(func() {
		p := rpio.Pin(pin)
		p.Output()
		p.Toggle()
	})
}

func memmapDo(op func()) error {
	if err := rpio.Open(); err != nil {
		return fmt.Errorf("memory map GPIO via /dev/mem is not supported: %v", err)
	}
	defer rpio.Close()

	op()

	return nil
}
