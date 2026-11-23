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

			err := tt.button.Init(context.Background())
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
				return mock.HighCalled && !mock.LowCalled
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

			err := tt.button.Deinit(context.Background())
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

func TestButtonMessageNamesTheButton(t *testing.T) {
	tests := []struct {
		button Button
		want   string
	}{
		{Button{Pin: 9, Label: "Power button"}, "Power button (GPIO 9): pressed for 10ms\n"},
		{Button{Pin: 10}, "GPIO 10: pressed for 10ms\n"},
	}

	for _, tt := range tests {
		tt.button.gpio = &MockGpio{}
		sesh := &mock.Session{}

		if err := tt.button.Run(context.Background(), sesh, "10ms"); err != nil {
			t.Fatalf("%s: %v", tt.button.label(), err)
		}

		if sesh.PrintText != tt.want {
			t.Errorf("%s printed %q, want %q", tt.button.label(), sesh.PrintText, tt.want)
		}
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
