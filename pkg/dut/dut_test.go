// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package dut

import (
	"errors"
	"reflect"
	"testing"
)

func TestNames(t *testing.T) {
	devs := Devlist{
		"device3": {},
		"device1": {},
		"device2": {},
	}

	result := devs.Names()

	expected := []string{"device1", "device2", "device3"}

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("expected %v, got %v", expected, result)
	}
}

func TestCmdNames(t *testing.T) {
	devs := Devlist{
		"device1": {Cmds: map[string]Command{
			"cmd3": {},
			"cmd1": {},
			"cmd2": {},
		}},
		"device2": {Cmds: map[string]Command{}},
	}

	tests := []struct {
		name     string
		device   string
		expected []string
		err      error
	}{
		{
			name:     "device found with commands",
			device:   "device1",
			expected: []string{"cmd1", "cmd2", "cmd3"},
			err:      nil,
		},
		{
			name:     "device found with no commands",
			device:   "device2",
			expected: []string{},
			err:      nil,
		},
		{
			name:     "device not found",
			device:   "device3",
			expected: []string{},
			err:      ErrDeviceNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := devs.CmdNames(tt.device)
			if !reflect.DeepEqual(result, tt.expected) || !errors.Is(err, tt.err) {
				t.Errorf("expected %v, %v; got %v, %v", tt.expected, tt.err, result, err)
			}
		})
	}
}

func TestFindCmd(t *testing.T) {
	devs := Devlist{
		"device1": Device{
			Cmds: map[string]Command{
				"cmd1": {
					Modules: []Module{
						{
							Config: ModuleConfig{
								Main: true,
							},
						},
					},
				},
				"cmd2": {
					Modules: []Module{
						{
							Config: ModuleConfig{},
						},
					},
				},
				"cmd3": {
					Modules: []Module{
						{
							Config: ModuleConfig{
								Main: true,
							},
						},
						{
							Config: ModuleConfig{
								Main: true,
							},
						},
					},
				},
			},
		},
	}

	tests := []struct {
		name    string
		device  string
		command string
		wantDev Device
		wantCmd Command
		err     error
	}{
		{
			name:    "device and command found",
			device:  "device1",
			command: "cmd1",
			wantDev: devs["device1"],
			wantCmd: devs["device1"].Cmds["cmd1"],
			err:     nil,
		},
		{
			name:    "device found, command not found",
			device:  "device1",
			command: "cmd4",
			wantDev: devs["device1"],
			wantCmd: Command{},
			err:     ErrCommandNotFound,
		},
		{
			name:    "device not found",
			device:  "device2",
			command: "cmd1",
			wantDev: Device{},
			wantCmd: Command{},
			err:     ErrDeviceNotFound,
		},
		{
			name:    "invalid command with no modules",
			device:  "device1",
			command: "cmd2",
			wantDev: devs["device1"],
			wantCmd: devs["device1"].Cmds["cmd2"],
			err:     ErrInvalidCommand,
		},
		{
			name:    "invalid command with multiple main modules",
			device:  "device1",
			command: "cmd3",
			wantDev: devs["device1"],
			wantCmd: devs["device1"].Cmds["cmd3"],
			err:     ErrInvalidCommand,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resultDev, resultCmd, err := devs.FindCmd(tt.device, tt.command)
			if !reflect.DeepEqual(resultDev, tt.wantDev) || !reflect.DeepEqual(resultCmd, tt.wantCmd) || !errors.Is(err, tt.err) {
				t.Errorf("expected %v, %v, %v; got %v, %v, %v", tt.wantDev, tt.wantCmd, tt.err, resultDev, resultCmd, err)
			}
		})
	}
}
