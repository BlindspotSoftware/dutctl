// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gpio

import (
	"fmt"
	"time"

	"github.com/stianeikeland/go-rpio/v4"
)

type devmemButton struct {
	pin rpio.Pin
}

func (b *devmemButton) Init(p Pin, idle State) error {
	if err := rpio.Open(); err != nil {
		return fmt.Errorf("memory map GPIO via /dev/mem is not supported: %v", err)
	}
	defer rpio.Close()

	b.pin = rpio.Pin(p)
	b.pin.Output()

	if idle == Low {
		b.pin.Low()
	} else {
		b.pin.High()
	}

	return nil
}

func (b *devmemButton) Press(duration time.Duration) error {
	if err := rpio.Open(); err != nil {
		return fmt.Errorf("memory map GPIO via /dev/mem is not supported: %v", err)
	}
	defer rpio.Close()

	b.pin.Toggle()
	time.Sleep(duration)
	b.pin.Toggle()

	return nil
}

func (b *devmemButton) Deinit() error {
	if err := rpio.Open(); err != nil {
		return fmt.Errorf("memory map GPIO via /dev/mem is not supported: %v", err)
	}
	defer rpio.Close()

	b.pin.Input()

	return nil
}

type devmemSwitch struct {
	pin       rpio.Pin
	activeLow bool
}

func (s *devmemSwitch) Init(p Pin, initial State, activeLow bool) error {
	if err := rpio.Open(); err != nil {
		return fmt.Errorf("memory map GPIO via /dev/mem is not supported: %v", err)
	}
	defer rpio.Close()

	s.pin = rpio.Pin(p)
	s.pin.Output()

	if initial == Low {
		if activeLow {
			s.pin.High()
		} else {
			s.pin.Low()
		}
	} else {
		if activeLow {
			s.pin.Low()
		} else {
			s.pin.High()
		}
	}

	return nil
}

func (s *devmemSwitch) On() error {
	if err := rpio.Open(); err != nil {
		return fmt.Errorf("memory map GPIO via /dev/mem is not supported: %v", err)
	}
	defer rpio.Close()

	if s.activeLow {
		s.pin.Low()
	} else {
		s.pin.High()
	}

	return nil
}

func (s *devmemSwitch) Off() error {
	if err := rpio.Open(); err != nil {
		return fmt.Errorf("memory map GPIO via /dev/mem is not supported: %v", err)
	}
	defer rpio.Close()

	if s.activeLow {
		s.pin.High()
	} else {
		s.pin.Low()
	}

	return nil
}

func (s *devmemSwitch) Toggle() error {
	if err := rpio.Open(); err != nil {
		return fmt.Errorf("memory map GPIO via /dev/mem is not supported: %v", err)
	}
	defer rpio.Close()

	s.pin.Toggle()

	return nil
}

func (s *devmemSwitch) Deinit() error {
	if err := rpio.Open(); err != nil {
		return fmt.Errorf("memory map GPIO via /dev/mem is not supported: %v", err)
	}
	defer rpio.Close()

	s.pin.Input()

	return nil
}
