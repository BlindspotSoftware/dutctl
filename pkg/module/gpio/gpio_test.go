// Copyright 2024 Blindspot Software
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

type MockGpio struct {
	LowCalled    bool
	HighCalled   bool
	ToggleCalled bool

	err error
}

func (m *MockGpio) Low(pin Pin) error {
	m.LowCalled = true
	if m.err != nil {
		return m.err
	}
	return nil
}

func (m *MockGpio) High(pin Pin) error {
	m.HighCalled = true
	if m.err != nil {
		return m.err
	}
	return nil
}

func (m *MockGpio) Toggle(pin Pin) error {
	m.ToggleCalled = true
	if m.err != nil {
		return m.err
	}
	return nil
}

func mockBackend(_ string) gpio {
	return &MockGpio{}
}

func mockErrBackend(_ string) gpio {
	return &MockGpio{
		err: errors.New("fake GPIO error"),
	}
}

func TestButtonInit(t *testing.T) {
	tests := []struct {
		name       string
		button     Button
		mockErr    error
		expectFunc func(mock *MockGpio) bool
		expectErr  bool
	}{
		{
			name:   "Init",
			button: Button{},
			expectFunc: func(mock *MockGpio) bool {
				return mock.LowCalled
			},
		},
		{
			name: "Init with ActiveLow",
			button: Button{
				ActiveLow: true,
			},
			expectFunc: func(mock *MockGpio) bool {
				return mock.HighCalled
			},
		},
		{
			name: "Init with error",
			button: Button{
				Pin: 1,
			},
			mockErr: errors.New("fake GPIO error"),
			expectFunc: func(mock *MockGpio) bool {
				return mock.LowCalled
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockErr != nil {
				tt.button.backendParser = mockErrBackend
			} else {
				tt.button.backendParser = mockBackend
			}

			err := tt.button.Init()
			if (err != nil) != tt.expectErr {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.expectFunc != nil && !tt.expectFunc(tt.button.gpio.(*MockGpio)) {
				t.Errorf("expected function not called for %s", tt.name)
			}
		})
	}
}

func TestButtonDeinit(t *testing.T) {
	tests := []struct {
		name       string
		button     Button
		mockErr    error
		expectFunc func(mock *MockGpio) bool
		expectErr  bool
	}{
		{
			name:   "Deinit",
			button: Button{},
			expectFunc: func(mock *MockGpio) bool {
				return mock.LowCalled
			},
		},
		{
			name: "Deinit with ActiveLow",
			button: Button{
				ActiveLow: true,
			},
			expectFunc: func(mock *MockGpio) bool {
				return mock.LowCalled
			},
		},
		{
			name: "Deinit with error",
			button: Button{
				Pin: 1,
			},
			mockErr: errors.New("fake GPIO error"),
			expectFunc: func(mock *MockGpio) bool {
				return mock.LowCalled
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockGpio := &MockGpio{
				err: tt.mockErr,
			}
			tt.button.gpio = mockGpio

			err := tt.button.Deinit()
			if (err != nil) != tt.expectErr {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.expectFunc != nil && !tt.expectFunc(mockGpio) {
				t.Errorf("expected function not called for %s", tt.name)
			}
		})
	}
}

func TestButtonRun(t *testing.T) {
	tests := []struct {
		name       string
		button     Button
		args       []string
		mockErr    error
		expectFunc func(mock *MockGpio) bool
		expectErr  bool
	}{
		{
			name:   "Run with default duration",
			button: Button{},
			expectFunc: func(mock *MockGpio) bool {
				return mock.ToggleCalled
			},
		},
		{
			name:   "Run with custom duration",
			button: Button{},
			args:   []string{"1s"},
			expectFunc: func(mock *MockGpio) bool {
				return mock.ToggleCalled
			},
		},
		{
			name:      "Run with invalid duration",
			button:    Button{},
			args:      []string{"two seconds"},
			expectErr: true,
		},
		{
			name:    "Run with error",
			button:  Button{},
			mockErr: errors.New("fake GPIO error"),
			expectFunc: func(mock *MockGpio) bool {
				return mock.ToggleCalled
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockGpio := &MockGpio{
				err: tt.mockErr,
			}
			tt.button.gpio = mockGpio
			ctx := context.Background()
			sesh := &mock.Session{}

			err := tt.button.Run(ctx, sesh, tt.args...)
			if (err != nil) != tt.expectErr {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.expectFunc != nil && !tt.expectFunc(mockGpio) {
				t.Errorf("expected function not called for %s", tt.name)
			}
		})
	}
}

func TestButtonHelp(t *testing.T) {
	tests := []struct {
		name          string
		button        Button
		expectStrings []string
	}{
		{
			name:   "Default Help",
			button: Button{},
			expectStrings: []string{
				"button",
				"duration",
			},
		},
		{
			name: "Help with ActiveLow",
			button: Button{
				ActiveLow: true,
			},
			expectStrings: []string{
				"button",
				"duration",
				"active low",
			},
		},
		{
			name: "Help contains pin number",
			button: Button{
				Pin: 12,
			},
			expectStrings: []string{
				"button",
				"duration",
				"12",
			},
		},
		{
			name: "Help contains pin number",
			button: Button{
				Pin: 99,
			},
			expectStrings: []string{
				"button",
				"duration",
				"99",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helpStr := strings.ToLower(tt.button.Help())
			for _, expectStr := range tt.expectStrings {
				if !strings.Contains(helpStr, strings.ToLower(expectStr)) {
					t.Errorf("expected string %q not found in help output", expectStr)
				}
			}
		})
	}
}

func TestSwitchInit(t *testing.T) {
	tests := []struct {
		name       string
		swtch      Switch
		mockErr    error
		expectFunc func(mock *MockGpio) bool
		expectErr  bool
	}{
		{
			name: "Init with Initial 'on' and ActiveLow false",
			swtch: Switch{
				Initial:   "on",
				ActiveLow: false,
			},
			expectFunc: func(mock *MockGpio) bool {
				return mock.HighCalled
			},
		},
		{
			name: "Init with Initial 'On' and ActiveLow false",
			swtch: Switch{
				Initial:   "On",
				ActiveLow: false,
			},
			expectFunc: func(mock *MockGpio) bool {
				return mock.HighCalled
			},
		},
		{
			name: "Init with Initial 'off' and ActiveLow false",
			swtch: Switch{
				Initial:   "off",
				ActiveLow: false,
			},
			expectFunc: func(mock *MockGpio) bool {
				return mock.LowCalled
			},
		},
		{
			name: "Init with Initial 'Off' and ActiveLow false",
			swtch: Switch{
				Initial:   "Off",
				ActiveLow: false,
			},
			expectFunc: func(mock *MockGpio) bool {
				return mock.LowCalled
			},
		},
		{
			name: "Init with Initial 'on' and ActiveLow true",
			swtch: Switch{
				Initial:   "on",
				ActiveLow: true,
			},
			expectFunc: func(mock *MockGpio) bool {
				return mock.LowCalled
			},
		},
		{
			name: "Init with Initial 'On' and ActiveLow true",
			swtch: Switch{
				Initial:   "On",
				ActiveLow: true,
			},
			expectFunc: func(mock *MockGpio) bool {
				return mock.LowCalled
			},
		},
		{
			name: "Init with Initial 'off' and ActiveLow true",
			swtch: Switch{
				Initial:   "off",
				ActiveLow: true,
			},
			expectFunc: func(mock *MockGpio) bool {
				return mock.HighCalled
			},
		},
		{
			name: "Init with Initial 'Off' and ActiveLow true",
			swtch: Switch{
				Initial:   "Off",
				ActiveLow: true,
			},
			expectFunc: func(mock *MockGpio) bool {
				return mock.HighCalled
			},
		},
		{
			name: "Init with Initial '' and ActiveLow false",
			swtch: Switch{
				Initial:   "",
				ActiveLow: false,
			},
			expectFunc: func(mock *MockGpio) bool {
				return mock.LowCalled
			},
		},
		{
			name: "Init with Initial 'unknown' and ActiveLow false",
			swtch: Switch{
				Initial:   "unknown",
				ActiveLow: false,
			},
			expectFunc: func(mock *MockGpio) bool {
				return mock.LowCalled
			},
		},
		{
			name: "Init with Initial '' and ActiveLow true",
			swtch: Switch{
				Initial:   "",
				ActiveLow: true,
			},
			expectFunc: func(mock *MockGpio) bool {
				return mock.HighCalled
			},
		},
		{
			name: "Init with Initial 'unknown' and ActiveLow true",
			swtch: Switch{
				Initial:   "unknown",
				ActiveLow: true,
			},
			expectFunc: func(mock *MockGpio) bool {
				return mock.HighCalled
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.swtch.backendParser = mockBackend

			err := tt.swtch.Init()
			if (err != nil) != tt.expectErr {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.expectFunc != nil && !tt.expectFunc(tt.swtch.gpio.(*MockGpio)) {
				t.Errorf("expected function not called for %s", tt.name)
			}
		})
	}
}

func TestSwitchDeinit(t *testing.T) {
	tests := []struct {
		name       string
		swtch      Switch
		mockErr    error
		expectFunc func(mock *MockGpio) bool
		expectErr  bool
	}{
		{
			name:  "Deinit",
			swtch: Switch{},
			expectFunc: func(mock *MockGpio) bool {
				return mock.LowCalled
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockGpio := &MockGpio{
				err: tt.mockErr,
			}
			tt.swtch.gpio = mockGpio

			err := tt.swtch.Deinit()
			if (err != nil) != tt.expectErr {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.expectFunc != nil && !tt.expectFunc(mockGpio) {
				t.Errorf("expected function not called for %s", tt.name)
			}
		})
	}
}

func TestSwitchRun(t *testing.T) {
	tests := []struct {
		name       string
		swtch      Switch
		args       []string
		mockErr    error
		expectFunc func(mock *MockGpio) bool
		expectErr  bool
	}{
		{
			name: "Run with state 'on', ActiveLow false, and args 'on'",
			swtch: Switch{
				state:     on,
				ActiveLow: false,
			},
			args: []string{"on"},
			expectFunc: func(mock *MockGpio) bool {
				return mock.HighCalled
			},
		},
		{
			name: "Run with state 'on', ActiveLow false, and args 'off'",
			swtch: Switch{
				state:     on,
				ActiveLow: false,
			},
			args: []string{"off"},
			expectFunc: func(mock *MockGpio) bool {
				return mock.LowCalled
			},
		},
		{
			name: "Run with state 'on', ActiveLow false, and args 'toggle'",
			swtch: Switch{
				state:     on,
				ActiveLow: false,
			},
			args: []string{"toggle"},
			expectFunc: func(mock *MockGpio) bool {
				return mock.ToggleCalled
			},
		},
		{
			name: "Run with state 'on', ActiveLow false, and args 'unknown'",
			swtch: Switch{
				state:     on,
				ActiveLow: false,
			},
			args:      []string{"unknown"},
			expectErr: true,
		},
		{
			name: "Run with state 'on', ActiveLow false, and args ''",
			swtch: Switch{
				state:     on,
				ActiveLow: false,
			},
			args:      []string{""},
			expectErr: true,
		},
		{
			name: "Run with state 'on', ActiveLow false, and empty args",
			swtch: Switch{
				state:     on,
				ActiveLow: false,
			},
			expectErr: false,
		},
		{
			name: "Run with state 'off', ActiveLow true, and args 'on'",
			swtch: Switch{
				state:     off,
				ActiveLow: true,
			},
			args: []string{"on"},
			expectFunc: func(mock *MockGpio) bool {
				return mock.LowCalled
			},
		},
		{
			name: "Run with state 'off', ActiveLow true, and args 'off'",
			swtch: Switch{
				state:     off,
				ActiveLow: true,
			},
			args: []string{"off"},
			expectFunc: func(mock *MockGpio) bool {
				return mock.HighCalled
			},
		},
		{
			name: "Run with state 'off', ActiveLow true, and args 'toggle'",
			swtch: Switch{
				state:     off,
				ActiveLow: true,
			},
			args: []string{"toggle"},
			expectFunc: func(mock *MockGpio) bool {
				return mock.ToggleCalled
			},
		},
		{
			name: "Run with state 'off', ActiveLow true, and args 'unknown'",
			swtch: Switch{
				state:     off,
				ActiveLow: true,
			},
			args:      []string{"unknown"},
			expectErr: true,
		},
		{
			name: "Run with state 'off', ActiveLow true, and args ''",
			swtch: Switch{
				state:     off,
				ActiveLow: true,
			},
			args:      []string{""},
			expectErr: true,
		},
		{
			name: "Run with state 'off', ActiveLow true, and empty args",
			swtch: Switch{
				state:     off,
				ActiveLow: true,
			},
			expectErr: false,
		},
		{
			name: "Run with args 'on' with GPIO error",
			swtch: Switch{
				state: off,
			},
			args:    []string{"on"},
			mockErr: errors.New("fake GPIO error"),
			expectFunc: func(mock *MockGpio) bool {
				return mock.HighCalled
			},
			expectErr: true,
		},
		{
			name: "Run with args 'off' with GPIO error",
			swtch: Switch{
				state: on,
			},
			args:    []string{"off"},
			mockErr: errors.New("fake GPIO error"),
			expectFunc: func(mock *MockGpio) bool {
				return mock.LowCalled
			},
			expectErr: true,
		},
		{
			name:    "Run with args 'toggle' with GPIO error",
			swtch:   Switch{},
			args:    []string{"toggle"},
			mockErr: errors.New("fake GPIO error"),
			expectFunc: func(mock *MockGpio) bool {
				return mock.ToggleCalled
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockGpio := &MockGpio{
				err: tt.mockErr,
			}
			tt.swtch.gpio = mockGpio
			ctx := context.Background()
			sesh := &mock.Session{}

			err := tt.swtch.Run(ctx, sesh, tt.args...)
			if (err != nil) != tt.expectErr {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.expectFunc != nil && !tt.expectFunc(mockGpio) {
				t.Errorf("expected function not called for %s", tt.name)
			}
		})
	}
}

func TestSwitchHelp(t *testing.T) {
	tests := []struct {
		name          string
		swtch         Switch
		expectStrings []string
	}{
		{
			name:  "Default Help",
			swtch: Switch{},
			expectStrings: []string{
				"switch",
				"on",
				"off",
				"toggle",
			},
		},
		{
			name: "Help with ActiveLow",
			swtch: Switch{
				ActiveLow: true,
			},
			expectStrings: []string{
				"switch",
				"on",
				"off",
				"toggle",
				"active low",
			},
		},
		{
			name: "Help contains pin number",
			swtch: Switch{
				Pin: 12,
			},
			expectStrings: []string{
				"switch",
				"on",
				"off",
				"toggle",
				"12",
			},
		},
		{
			name: "Help contains pin number",
			swtch: Switch{
				Pin: 99,
			},
			expectStrings: []string{
				"switch",
				"on",
				"off",
				"toggle",
				"99",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helpStr := strings.ToLower(tt.swtch.Help())
			for _, expectStr := range tt.expectStrings {
				if !strings.Contains(helpStr, strings.ToLower(expectStr)) {
					t.Errorf("expected string %q not found in help output", expectStr)
				}
			}
		})
	}
}

func TestBackendFromOption(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected gpio
	}{
		{
			name:     "Valid backend 'devmem'",
			input:    "devmem",
			expected: &devmem{},
		},
		{
			name:     "Invalid backend defaults to 'devmem'",
			input:    "unknown",
			expected: &devmem{},
		},
		{
			name:     "Empty backend defaults to 'devmem'",
			input:    "",
			expected: &devmem{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := backendFromOption(tt.input)
			if _, ok := result.(*devmem); !ok {
				t.Errorf("expected type *devmem, got %T", result)
			}
		})
	}
}
