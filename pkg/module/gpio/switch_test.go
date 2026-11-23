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

func TestSwitchInit(t *testing.T) {
	tests := []struct {
		name          string
		swtch         Switch
		mockErr       error
		expectFunc    func(mock *MockGpio) bool
		expectErr     bool
		expectedState switchState
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
			expectedState: on,
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
			expectedState: on,
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
			expectedState: off,
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
			expectedState: off,
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
			expectedState: on,
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
			expectedState: on,
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
			expectedState: off,
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
			expectedState: off,
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
			expectedState: off,
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
			expectedState: off,
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
			expectedState: off,
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
			expectedState: off,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.swtch.backendParser = mockBackend

			err := tt.swtch.Init(context.Background())
			if (err != nil) != tt.expectErr {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.expectFunc != nil && !tt.expectFunc(tt.swtch.gpio.(*MockGpio)) {
				t.Errorf("expected function not called for %s", tt.name)
			}
			if tt.swtch.state != tt.expectedState {
				t.Errorf("expected state %q after Init, got %q", tt.expectedState, tt.swtch.state)
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
		{
			name: "Deinit with ActiveLow",
			swtch: Switch{
				ActiveLow: true,
			},
			expectFunc: func(mock *MockGpio) bool {
				return mock.HighCalled && !mock.LowCalled
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockGpio := &MockGpio{
				err: tt.mockErr,
			}
			tt.swtch.gpio = mockGpio

			err := tt.swtch.Deinit(context.Background())
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
			if !tt.expectErr && !strings.HasSuffix(sesh.PrintText, "\n") {
				t.Errorf("expected output to end with a newline, got %q", sesh.PrintText)
			}
		})
	}
}

func TestSwitchMessagesNameTheSwitch(t *testing.T) {
	tests := []struct {
		swtch Switch
		arg   string
		want  string
	}{
		{Switch{Pin: 17, Label: "PSU 1 socket", state: on}, "on", "PSU 1 socket (GPIO 17): already on\n"},
		{Switch{Pin: 17, Label: "PSU 1 socket", state: on}, "off", "PSU 1 socket (GPIO 17): turned off\n"},
		{Switch{Pin: 26, state: off}, "on", "GPIO 26: turned on\n"},
		{Switch{Pin: 26, state: off}, "off", "GPIO 26: already off\n"},
		{Switch{Pin: 26, state: on}, "toggle", "GPIO 26: turned off\n"},
	}

	for _, tt := range tests {
		tt.swtch.gpio = &MockGpio{}
		sesh := &mock.Session{}

		if err := tt.swtch.Run(context.Background(), sesh, tt.arg); err != nil {
			t.Fatalf("%s %s: %v", tt.swtch.label(), tt.arg, err)
		}

		if sesh.PrintText != tt.want {
			t.Errorf("%s %s printed %q, want %q", tt.swtch.label(), tt.arg, sesh.PrintText, tt.want)
		}
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
