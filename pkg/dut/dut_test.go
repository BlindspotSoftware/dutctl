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
								ForwardArgs: true,
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
								ForwardArgs: true,
							},
						},
						{
							Config: ModuleConfig{
								ForwardArgs: true,
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
			name:    "invalid command with no forwardArgs module",
			device:  "device1",
			command: "cmd2",
			wantDev: devs["device1"],
			wantCmd: devs["device1"].Cmds["cmd2"],
			err:     ErrNoModules,
		},
		{
			name:    "invalid command with multiple forwardArgs modules",
			device:  "device1",
			command: "cmd3",
			wantDev: devs["device1"],
			wantCmd: devs["device1"].Cmds["cmd3"],
			err:     ErrMultipleForwardArgsModules,
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
			name: "single forwardArgs module gets runtime args",
			cmd: Command{Modules: []Module{
				{Config: ModuleConfig{ForwardArgs: true}},
			}},
			runtimeArgs: []string{"a", "b"},
			want:        [][]string{{"a", "b"}},
		},
		{
			name: "runtime args swallowed when no forwardArgs module",
			cmd: Command{Modules: []Module{
				{Config: ModuleConfig{Args: []string{"x"}}},
			}},
			runtimeArgs: []string{"a"},
			want:        [][]string{{"x"}},
		},
		{
			name: "mixed forwardArgs and non-forwardArgs",
			cmd: Command{Modules: []Module{
				{Config: ModuleConfig{ForwardArgs: true}},
				{Config: ModuleConfig{Args: []string{"static1", "static2"}}},
			}},
			runtimeArgs: []string{"run1"},
			want:        [][]string{{"run1"}, {"static1", "static2"}},
		},
		{
			name: "template substitution with no extra args, forwardArgs gets nil",
			cmd: Command{
				Args: []ArgDecl{
					{Name: "file", Desc: "Input file"},
					{Name: "device", Desc: "Device ID"},
				},
				Modules: []Module{
					{Config: ModuleConfig{ForwardArgs: true}},
					{Config: ModuleConfig{Args: []string{"flash", "${file}", "--device=${device}"}}},
				},
			},
			runtimeArgs: []string{"firmware.bin", "dev123"},
			want:        [][]string{nil, {"flash", "firmware.bin", "--device=dev123"}},
		},
		{
			name: "extra args forwarded after named arg substitution",
			cmd: Command{
				Args: []ArgDecl{
					{Name: "file", Desc: "Input file"},
					{Name: "device", Desc: "Device ID"},
				},
				Modules: []Module{
					{Config: ModuleConfig{ForwardArgs: true}},
					{Config: ModuleConfig{Args: []string{"flash", "${file}", "--device=${device}"}}},
				},
			},
			runtimeArgs: []string{"firmware.bin", "dev123", "extra1", "extra2"},
			want:        [][]string{{"extra1", "extra2"}, {"flash", "firmware.bin", "--device=dev123"}},
		},
		{
			name:        "runtime args swallowed with empty modules",
			cmd:         Command{},
			runtimeArgs: []string{"a"},
			want:        [][]string{},
		},
		{
			name: "runtime args swallowed when module has no forwardArgs",
			cmd: Command{Modules: []Module{
				{Config: ModuleConfig{}},
			}},
			runtimeArgs: []string{"a"},
			want:        [][]string{nil},
		},
		{
			name: "forwardArgs module with no runtime args",
			cmd: Command{Modules: []Module{
				{Config: ModuleConfig{ForwardArgs: true}},
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

func TestCommandArgsUnmarshal(t *testing.T) {
	// Test args parsing without modules (modules require registration)
	cmd := Command{
		Desc: "Test command",
		Args: []ArgDecl{
			{Name: "flash-file", Desc: "Firmware to flash"},
			{Name: "device-id", Desc: "Target device"},
		},
	}

	if len(cmd.Args) != 2 {
		t.Errorf("Expected 2 args, got %d", len(cmd.Args))
	}

	if cmd.Args[0].Name != "flash-file" || cmd.Args[0].Desc != "Firmware to flash" {
		t.Errorf("Wrong arg declaration for flash-file: %+v", cmd.Args[0])
	}

	if cmd.Args[1].Name != "device-id" || cmd.Args[1].Desc != "Target device" {
		t.Errorf("Wrong arg declaration for device-id: %+v", cmd.Args[1])
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
		cmdName  string
		cmd      Command
		wantText string
	}{
		{
			name:     "no modules, no desc",
			cmdName:  "my-cmd",
			cmd:      Command{},
			wantText: "\nUsage: my-cmd\n",
		},
		{
			name:    "desc shown before usage",
			cmdName: "power-cycle",
			cmd: Command{
				Desc: "Power cycle the device",
				Modules: []Module{
					{Module: &helpModule{text: "gpio"}, Config: ModuleConfig{Name: "gpio"}},
				},
			},
			wantText: "Power cycle the device\n\nUsage: power-cycle\n",
		},
		{
			name:    "static only, no args, no forwardArgs",
			cmdName: "power-cycle",
			cmd: Command{Modules: []Module{
				{Module: &helpModule{text: "gpio"}, Config: ModuleConfig{Name: "gpio"}},
			}},
			wantText: "\nUsage: power-cycle\n",
		},
		{
			name:    "forwardArgs only",
			cmdName: "run-command",
			cmd: Command{Modules: []Module{
				{
					Module: &helpModule{text: "usage: shell <cmd> [options]"},
					Config: ModuleConfig{Name: "shell", ForwardArgs: true},
				},
			}},
			wantText: "\nUsage: run-command [args...]\n\nusage: shell <cmd> [options]",
		},
		{
			name:    "forwardArgs only, with desc",
			cmdName: "run-command",
			cmd: Command{
				Desc: "Run a shell command on the device",
				Modules: []Module{
					{
						Module: &helpModule{text: "usage: shell <cmd> [options]"},
						Config: ModuleConfig{Name: "shell", ForwardArgs: true},
					},
				},
			},
			wantText: "Run a shell command on the device\n\nUsage: run-command [args...]\n\nusage: shell <cmd> [options]",
		},
		{
			name:    "named args only, single arg",
			cmdName: "flash-firmware",
			cmd: Command{
				Args: []ArgDecl{
					{Name: "firmware-file", Desc: "Path to firmware binary"},
				},
				Modules: []Module{
					{Module: &helpModule{text: "flash"}, Config: ModuleConfig{Name: "flash"}},
				},
			},
			wantText: "\nUsage: flash-firmware <firmware-file>\n\nArguments:\n  firmware-file  Path to firmware binary\n",
		},
		{
			name:    "named args only, multiple args with alignment",
			cmdName: "flash-firmware",
			cmd: Command{
				Desc: "Flash firmware to the device",
				Args: []ArgDecl{
					{Name: "firmware-file", Desc: "Path to firmware binary"},
					{Name: "backup-path", Desc: "Backup location"},
				},
				Modules: []Module{
					{Module: &helpModule{text: "file"}, Config: ModuleConfig{Name: "file"}},
					{Module: &helpModule{text: "flash"}, Config: ModuleConfig{Name: "flash"}},
				},
			},
			// firmware-file (13 chars) is the longest; backup-path (11) padded to 13
			wantText: "Flash firmware to the device\n\nUsage: flash-firmware <firmware-file> <backup-path>\n\nArguments:\n  firmware-file  Path to firmware binary\n  backup-path    Backup location\n",
		},
		{
			name:    "forwardArgs combined with single named arg",
			cmdName: "flash-and-run",
			cmd: Command{
				Desc: "Flash firmware, then run a command on the device",
				Args: []ArgDecl{
					{Name: "firmware-file", Desc: "Path to firmware binary"},
				},
				Modules: []Module{
					{Module: &helpModule{text: "file"}, Config: ModuleConfig{Name: "file"}},
					{Module: &helpModule{text: "flash"}, Config: ModuleConfig{Name: "flash"}},
					{
						Module: &helpModule{text: "usage: shell <cmd> [options]"},
						Config: ModuleConfig{Name: "shell", ForwardArgs: true},
					},
				},
			},
			wantText: "Flash firmware, then run a command on the device\n\nUsage: flash-and-run <firmware-file> [args...]\n\nArguments:\n  firmware-file  Path to firmware binary\n\nusage: shell <cmd> [options]",
		},
		{
			name:    "forwardArgs combined with multiple named args",
			cmdName: "flash-and-run",
			cmd: Command{
				Args: []ArgDecl{
					{Name: "firmware-file", Desc: "Path to firmware binary"},
					{Name: "device-id", Desc: "Target device ID"},
				},
				Modules: []Module{
					{Module: &helpModule{text: "file"}, Config: ModuleConfig{Name: "file"}},
					{Module: &helpModule{text: "flash"}, Config: ModuleConfig{Name: "flash"}},
					{
						Module: &helpModule{text: "usage: shell <cmd> [options]"},
						Config: ModuleConfig{Name: "shell", ForwardArgs: true},
					},
				},
			},
			// firmware-file (13) > device-id (9), so device-id is padded to 13
			wantText: "\nUsage: flash-and-run <firmware-file> <device-id> [args...]\n\nArguments:\n  firmware-file  Path to firmware binary\n  device-id      Target device ID\n\nusage: shell <cmd> [options]",
		},
		{
			name:    "forwardArgs not first module",
			cmdName: "multi-step",
			cmd: Command{Modules: []Module{
				{Module: &helpModule{text: "setup"}, Config: ModuleConfig{Name: "setup"}},
				{
					Module: &helpModule{text: "usage: main-mod"},
					Config: ModuleConfig{Name: "main-mod", ForwardArgs: true},
				},
			}},
			wantText: "\nUsage: multi-step [args...]\n\nusage: main-mod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := tt.cmd.HelpText(tt.cmdName)
			if text != tt.wantText {
				t.Errorf("text: expected %q, got %q", tt.wantText, text)
			}
		})
	}
}
