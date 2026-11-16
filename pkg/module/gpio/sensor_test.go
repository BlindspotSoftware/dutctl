// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gpio

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
)

func TestSensorInitSetsPull(t *testing.T) {
	tests := []struct {
		pull string
		want pull
	}{
		{"", pullOff},
		{"off", pullOff},
		{"up", pullUp},
		{"Up", pullUp},
		{"down", pullDown},
		{"DOWN", pullDown},
	}

	for _, tt := range tests {
		sn := Sensor{Pull: tt.pull, backendParser: mockBackend}

		if err := sn.Init(context.Background()); err != nil {
			t.Fatalf("pull %q: %v", tt.pull, err)
		}

		m := sn.gpio.(*MockGpio)
		if !m.InputCalled {
			t.Errorf("pull %q: Init did not set the pin to input", tt.pull)
		}

		if m.inputPull != tt.want {
			t.Errorf("pull %q: Init set pull %d, want %d", tt.pull, m.inputPull, tt.want)
		}

		if m.LowCalled || m.HighCalled || m.ToggleCalled {
			t.Errorf("pull %q: Init drove the input pin", tt.pull)
		}
	}
}

func TestSensorInitRejectsPinOutOfRange(t *testing.T) {
	for _, pin := range []Pin{58, 64, 255} {
		sn := Sensor{Pin: pin, backendParser: mockBackend}

		if err := sn.Init(context.Background()); err == nil {
			t.Errorf("Init accepted pin %d", pin)
		}

		if sn.gpio != nil {
			t.Errorf("pin %d: Init touched the backend before rejecting the pin", pin)
		}
	}
}

func TestSensorInitAcceptsHighestPin(t *testing.T) {
	sn := Sensor{Pin: maxPin, backendParser: mockBackend}

	if err := sn.Init(context.Background()); err != nil {
		t.Fatalf("Init rejected pin %d: %v", maxPin, err)
	}

	if !sn.gpio.(*MockGpio).InputCalled {
		t.Errorf("pin %d: Init did not set the pin to input", maxPin)
	}
}

func TestSensorInitRejectsUnknownPull(t *testing.T) {
	sn := Sensor{Pull: "sideways", backendParser: mockBackend}

	if err := sn.Init(context.Background()); err == nil {
		t.Error("Init accepted pull \"sideways\"")
	}
}

func TestSensorInitError(t *testing.T) {
	sn := Sensor{backendParser: mockErrBackend}

	if err := sn.Init(context.Background()); err == nil {
		t.Error("Init swallowed the backend error")
	}
}

func TestSensorDeinitDrivesNothing(t *testing.T) {
	m := &MockGpio{}
	sn := Sensor{gpio: m}

	if err := sn.Deinit(context.Background()); err != nil {
		t.Fatal(err)
	}

	if m.LowCalled || m.HighCalled || m.ToggleCalled || m.InputCalled {
		t.Error("Deinit touched the input pin")
	}
}

func TestSensorRunPrintsLevel(t *testing.T) {
	tests := []struct {
		sensor Sensor
		level  State
		want   string
	}{
		{Sensor{Pin: 22, Label: "Power good"}, High, "Power good (GPIO 22): active (high)\n"},
		{Sensor{Pin: 22, Label: "Power good"}, Low, "Power good (GPIO 22): inactive (low)\n"},
		{Sensor{Pin: 5, ActiveLow: true}, Low, "GPIO 5: active (low)\n"},
		{Sensor{Pin: 5, ActiveLow: true}, High, "GPIO 5: inactive (high)\n"},
	}

	for _, tt := range tests {
		m := &MockGpio{level: tt.level}
		tt.sensor.gpio = m
		sesh := &mock.Session{}

		if err := tt.sensor.Run(context.Background(), sesh); err != nil {
			t.Fatalf("%s: %v", tt.sensor.label(), err)
		}

		if !m.ReadCalled {
			t.Errorf("%s: Run did not read the pin", tt.sensor.label())
		}

		if sesh.PrintText != tt.want {
			t.Errorf("%s printed %q, want %q", tt.sensor.label(), sesh.PrintText, tt.want)
		}
	}
}

func TestSensorRunRejectsArguments(t *testing.T) {
	m := &MockGpio{}
	sn := Sensor{gpio: m}

	if err := sn.Run(context.Background(), &mock.Session{}, "on"); err == nil {
		t.Error("Run accepted an argument")
	}

	if m.ReadCalled {
		t.Error("Run read the pin despite a bad argument")
	}
}

func TestSensorRunError(t *testing.T) {
	sn := Sensor{gpio: &MockGpio{err: errors.New("fake GPIO error")}}
	sesh := &mock.Session{}

	if err := sn.Run(context.Background(), sesh); err == nil {
		t.Error("Run swallowed the backend error")
	}

	if sesh.PrintText != "" {
		t.Errorf("Run printed %q despite the error", sesh.PrintText)
	}
}

func TestSensorHelp(t *testing.T) {
	tests := []struct {
		sensor Sensor
		want   []string
	}{
		{Sensor{Pin: 12}, []string{"signal", "active high", "12", "pull resistor is off."}},
		{Sensor{Pin: 7, ActiveLow: true}, []string{"signal", "active low", "7"}},
		{Sensor{Pin: 22, Pull: "Up"}, []string{"pull resistor is up."}},
		{Sensor{Pin: 23, Pull: "down"}, []string{"pull resistor is down."}},
	}

	for _, tt := range tests {
		help := strings.ToLower(tt.sensor.Help())
		for _, w := range tt.want {
			if !strings.Contains(help, w) {
				t.Errorf("help for pin %d lacks %q", tt.sensor.Pin, w)
			}
		}
	}
}
