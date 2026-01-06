// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package dut

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
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
					Modules: []Module{},
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
				"cmd4": {
					Modules: []Module{},
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
			command: "nonexistent",
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
			name:    "invalid command with no main module",
			device:  "device1",
			command: "cmd2",
			wantDev: devs["device1"],
			wantCmd: devs["device1"].Cmds["cmd2"],
			err:     ErrNoModules,
		},
		{
			name:    "invalid command with multiple main modules",
			device:  "device1",
			command: "cmd3",
			wantDev: devs["device1"],
			wantCmd: devs["device1"].Cmds["cmd3"],
			err:     ErrMultipleMainModules,
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

func TestModuleArgs(t *testing.T) {
	tests := []struct {
		name        string
		cmd         Command
		runtimeArgs []string
		want        [][]string
		err         error
	}{
		{
			name: "single main module gets runtime args",
			cmd: Command{Modules: []Module{
				{Config: ModuleConfig{Main: true}},
			}},
			runtimeArgs: []string{"a", "b"},
			want:        [][]string{{"a", "b"}},
		},
		{
			name: "non-main gets static args",
			cmd: Command{Modules: []Module{
				{Config: ModuleConfig{Args: []string{"x"}}},
			}},
			runtimeArgs: []string{"a"},
			want:        nil,
			err:         ErrNoMainForArgs,
		},
		{
			name: "mixed main and non-main",
			cmd: Command{Modules: []Module{
				{Config: ModuleConfig{Main: true}},
				{Config: ModuleConfig{Args: []string{"static1", "static2"}}},
			}},
			runtimeArgs: []string{"run1"},
			want:        [][]string{{"run1"}, {"static1", "static2"}},
		},
		{
			name:        "empty modules",
			cmd:         Command{},
			runtimeArgs: []string{"a"},
			want:        nil,
			err:         ErrNoMainForArgs,
		},
		{
			name: "error when runtime args provided but no main module",
			cmd: Command{Modules: []Module{
				{Config: ModuleConfig{}},
			}},
			runtimeArgs: []string{"a"},
			want:        nil,
			err:         ErrNoMainForArgs,
		},
		{
			name: "main module with no runtime args",
			cmd: Command{Modules: []Module{
				{Config: ModuleConfig{Main: true}},
			}},
			runtimeArgs: nil,
			want:        [][]string{nil},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.cmd.ModuleArgs(tt.runtimeArgs)
			if !reflect.DeepEqual(result, tt.want) {
				t.Errorf("expected %v, got %v", tt.want, result)
			}

			if tt.err != nil && !errors.Is(err, tt.err) {
				t.Errorf("expected error %v, got %v", tt.err, err)
			} else if tt.err == nil && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

// helpModule is a minimal test double implementing module.Module for HelpText tests.
type helpModule struct {
	text string
}

func (m *helpModule) Help() string                                               { return m.text }
func (m *helpModule) Init() error                                                { return nil }
func (m *helpModule) Deinit() error                                              { return nil }
func (m *helpModule) Run(_ context.Context, _ module.Session, _ ...string) error { return nil }

func TestHelpText(t *testing.T) {
	tests := []struct {
		name     string
		cmd      Command
		wantText string
	}{
		{
			name:     "no modules",
			cmd:      Command{},
			wantText: "Command with 0 module(s): ",
		},
		{
			name: "main module exists",
			cmd: Command{Modules: []Module{
				{
					Module: &helpModule{text: "usage: flash <image>"},
					Config: ModuleConfig{Name: "flash", Main: true},
				},
			}},
			wantText: "usage: flash <image>",
		},
		{
			name: "no main module",
			cmd: Command{Modules: []Module{
				{
					Module: &helpModule{text: "helper"},
					Config: ModuleConfig{Name: "helper"},
				},
			}},
			wantText: "Command with 1 module(s): helper",
		},
		{
			name: "multiple modules without main",
			cmd: Command{Modules: []Module{
				{
					Module: &helpModule{text: "setup"},
					Config: ModuleConfig{Name: "setup"},
				},
				{
					Module: &helpModule{text: "cleanup"},
					Config: ModuleConfig{Name: "cleanup"},
				},
			}},
			wantText: "Command with 2 module(s): setup, cleanup",
		},
		{
			name: "main among multiple modules",
			cmd: Command{Modules: []Module{
				{
					Module: &helpModule{text: "helper"},
					Config: ModuleConfig{Name: "helper"},
				},
				{
					Module: &helpModule{text: "main help"},
					Config: ModuleConfig{Name: "main", Main: true},
				},
			}},
			wantText: "main help",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := tt.cmd.HelpText()
			if text != tt.wantText {
				t.Errorf("text: expected %q, got %q", tt.wantText, text)
			}
		})
	}
}
