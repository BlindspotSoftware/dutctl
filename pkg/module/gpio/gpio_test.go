// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gpio

import (
	"errors"
	"testing"
)

type MockGpio struct {
	LowCalled    bool
	HighCalled   bool
	ToggleCalled bool
	InputCalled  bool
	ReadCalled   bool

	inputPull pull  // pull passed to Input
	level     State // level Read returns

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

func (m *MockGpio) Input(pin Pin, p pull) error {
	m.InputCalled = true
	m.inputPull = p

	return m.err
}

func (m *MockGpio) Read(pin Pin) (State, error) {
	m.ReadCalled = true
	if m.err != nil {
		return Low, m.err
	}

	return m.level, nil
}

func mockBackend(_ string) gpio {
	return &MockGpio{}
}

func mockErrBackend(_ string) gpio {
	return &MockGpio{
		err: errors.New("fake GPIO error"),
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
