// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdu

import (
	"testing"
)

func TestGudeCommandsString(t *testing.T) {
	tests := []struct {
		name     string
		cmd      gudeCommands
		expected string
	}{
		{
			name:     "switch command",
			cmd:      gudeSwitchCommand,
			expected: "1",
		},
		{
			name:     "batch mode command",
			cmd:      gudeBatchModeCommand,
			expected: "2",
		},
		{
			name:     "reset command",
			cmd:      gudeResetCommand,
			expected: "12",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.cmd.String()
			if result != tt.expected {
				t.Errorf("String() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestGudeStateString(t *testing.T) {
	tests := []struct {
		name     string
		state    gudeState
		expected string
	}{
		{
			name:     "state off",
			state:    gudeStateOff,
			expected: "off",
		},
		{
			name:     "state on",
			state:    gudeStateOn,
			expected: "on",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.state.String()
			if result != tt.expected {
				t.Errorf("String() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestGudeStateGetAPIParameter(t *testing.T) {
	tests := []struct {
		name     string
		state    gudeState
		expected string
	}{
		{
			name:     "state off returns 0",
			state:    gudeStateOff,
			expected: "0",
		},
		{
			name:     "state on returns 1",
			state:    gudeStateOn,
			expected: "1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.state.getAPIParameter()
			if result != tt.expected {
				t.Errorf("getAPIParameter() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestNewGudeStateFromInt(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected gudeState
		err      bool
	}{
		{
			name:     "0 returns state off",
			input:    0,
			expected: gudeStateOff,
			err:      false,
		},
		{
			name:     "1 returns state on",
			input:    1,
			expected: gudeStateOn,
			err:      false,
		},
		{
			name:     "2 returns error",
			input:    2,
			expected: -1,
			err:      true,
		},
		{
			name:     "negative value returns error",
			input:    -1,
			expected: -1,
			err:      true,
		},
		{
			name:     "large value returns error",
			input:    999,
			expected: -1,
			err:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := newGudeStateFromInt(tt.input)

			if tt.err {
				if err == nil {
					t.Errorf("newGudeStateFromInt() expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("newGudeStateFromInt() unexpected error: %v", err)
				return
			}

			if result != tt.expected {
				t.Errorf("newGudeStateFromInt() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestNewGudeStateFromString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected gudeState
		err      bool
	}{
		{
			name:     "on returns state on",
			input:    "on",
			expected: gudeStateOn,
			err:      false,
		},
		{
			name:     "off returns state off",
			input:    "off",
			expected: gudeStateOff,
			err:      false,
		},
		{
			name:     "invalid string returns error",
			input:    "invalid",
			expected: -1,
			err:      true,
		},
		{
			name:     "empty string returns error",
			input:    "",
			expected: -1,
			err:      true,
		},
		{
			name:     "toggle returns error",
			input:    "toggle",
			expected: -1,
			err:      true,
		},
		{
			name:     "On with capital returns error (case sensitive)",
			input:    "On",
			expected: -1,
			err:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := newGudeStateFromString(tt.input)

			if tt.err {
				if err == nil {
					t.Errorf("newGudeStateFromString() expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("newGudeStateFromString() unexpected error: %v", err)
				return
			}

			if result != tt.expected {
				t.Errorf("newGudeStateFromString() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestGudeParseOutletStatus(t *testing.T) {
	tests := []struct {
		name     string
		outlet   int
		jsonBody string
		expected int
		err      bool
	}{
		{
			name:   "outlet 0 off",
			outlet: 0,
			jsonBody: `{
				"outputs": [
					{"name": "Power Port", "state": 0, "sw_cnt": 8, "type": 1, "batch": [0,0,0,0,0,0], "wdog": [0,3,null,32]},
					{"name": "Power Port", "state": 1, "sw_cnt": 0, "type": 1, "batch": [0,0,0,0,0,0], "wdog": [0,3,null,32]}
				]
			}`,
			expected: 0,
			err:      false,
		},
		{
			name:   "outlet 1 on",
			outlet: 1,
			jsonBody: `{
				"outputs": [
					{"name": "Power Port", "state": 0, "sw_cnt": 8, "type": 1, "batch": [0,0,0,0,0,0], "wdog": [0,3,null,32]},
					{"name": "Power Port", "state": 1, "sw_cnt": 0, "type": 1, "batch": [0,0,0,0,0,0], "wdog": [0,3,null,32]}
				]
			}`,
			expected: 1,
			err:      false,
		},
		{
			name:   "real PDU response - 4 outlets",
			outlet: 2,
			jsonBody: `{
				"outputs": [
					{"name": "Power Port", "state": 0, "sw_cnt": 8, "type": 1, "batch": [0,0,0,0,0,0], "wdog": [0,3,null,32]},
					{"name": "Power Port", "state": 0, "sw_cnt": 0, "type": 1, "batch": [0,0,0,0,0,0], "wdog": [0,3,null,32]},
					{"name": "Power Port", "state": 1, "sw_cnt": 0, "type": 1, "batch": [0,0,0,0,0,0], "wdog": [0,3,null,32]},
					{"name": "Power Port", "state": 0, "sw_cnt": 0, "type": 1, "batch": [0,0,0,0,0,0], "wdog": [0,3,null,32]}
				]
			}`,
			expected: 1,
			err:      false,
		},
		{
			name:   "outlet not found - out of range",
			outlet: 5,
			jsonBody: `{
				"outputs": [
					{"name": "Power Port", "state": 0, "sw_cnt": 8, "type": 1, "batch": [0,0,0,0,0,0], "wdog": [0,3,null,32]},
					{"name": "Power Port", "state": 1, "sw_cnt": 0, "type": 1, "batch": [0,0,0,0,0,0], "wdog": [0,3,null,32]}
				]
			}`,
			expected: -1,
			err:      true,
		},
		{
			name:     "malformed JSON - missing closing brace",
			outlet:   0,
			jsonBody: `{"outputs": [{"name": "Power Port", "state": 0}`,
			expected: -1,
			err:      true,
		},
		{
			name:     "empty outputs array",
			outlet:   0,
			jsonBody: `{"outputs": []}`,
			expected: -1,
			err:      true,
		},
		{
			name:     "empty JSON",
			outlet:   0,
			jsonBody: ``,
			expected: -1,
			err:      true,
		},
		{
			name:     "invalid JSON - not an object",
			outlet:   0,
			jsonBody: `null`,
			expected: -1,
			err:      true,
		},
		{
			name:   "missing outputs field",
			outlet: 0,
			jsonBody: `{
				"other_field": "value"
			}`,
			expected: -1,
			err:      true,
		},
		{
			name:   "outlet at exact boundary - last outlet",
			outlet: 3,
			jsonBody: `{
				"outputs": [
					{"name": "Port 1", "state": 0, "sw_cnt": 0, "type": 1, "batch": [0,0,0,0,0,0], "wdog": [0,3,null,32]},
					{"name": "Port 2", "state": 1, "sw_cnt": 0, "type": 1, "batch": [0,0,0,0,0,0], "wdog": [0,3,null,32]},
					{"name": "Port 3", "state": 0, "sw_cnt": 0, "type": 1, "batch": [0,0,0,0,0,0], "wdog": [0,3,null,32]},
					{"name": "Port 4", "state": 1, "sw_cnt": 0, "type": 1, "batch": [0,0,0,0,0,0], "wdog": [0,3,null,32]}
				]
			}`,
			expected: 1,
			err:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &gude{
				pdu: &PDU{
					Outlet: tt.outlet,
				},
			}

			result, err := g.parseOutletStatus([]byte(tt.jsonBody))

			if tt.err {
				if err == nil {
					t.Errorf("parseOutletStatus() expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("parseOutletStatus() unexpected error: %v", err)
				return
			}

			if result != tt.expected {
				t.Errorf("parseOutletStatus() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestGudeGetOutletAPIParameter(t *testing.T) {
	tests := []struct {
		name     string
		outlet   int
		expected string
	}{
		{
			name:     "outlet 0 converts to 1",
			outlet:   0,
			expected: "1",
		},
		{
			name:     "outlet 1 converts to 2",
			outlet:   1,
			expected: "2",
		},
		{
			name:     "outlet 5 converts to 6",
			outlet:   5,
			expected: "6",
		},
		{
			name:     "outlet 99 converts to 100",
			outlet:   99,
			expected: "100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &gude{
				pdu: &PDU{
					Outlet: tt.outlet,
				},
			}

			result := g.getOutletAPIParameter()
			if result != tt.expected {
				t.Errorf("getOutletAPIParameter() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
