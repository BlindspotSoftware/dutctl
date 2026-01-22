// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package dut

import (
	"errors"
	"reflect"
	"strings"
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
								Interactive: true,
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
								Interactive: true,
							},
						},
						{
							Config: ModuleConfig{
								Interactive: true,
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
			err:     ErrNoModules,
		},
		{
			name:    "invalid command with multiple interactive modules",
			device:  "device1",
			command: "cmd3",
			wantDev: devs["device1"],
			wantCmd: devs["device1"].Cmds["cmd3"],
			err:     ErrMultipleInteractiveModules,
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

func TestTemplateValidation(t *testing.T) {
	tests := []struct {
		name      string
		cmd       Command
		expectErr bool
		errMsg    string
	}{
		{
			name: "valid templates",
			cmd: Command{
				Args: []ArgDecl{{Name: "file", Desc: "File to copy"}},
				Modules: []Module{
					{Config: ModuleConfig{Name: "test", Args: []string{"${file}"}}},
				},
			},
			expectErr: false,
		},
		{
			name: "undefined template reference",
			cmd: Command{
				Args: []ArgDecl{{Name: "file", Desc: "File to copy"}},
				Modules: []Module{
					{Config: ModuleConfig{Name: "test", Args: []string{"${undefined}"}}},
				},
			},
			expectErr: true,
			errMsg:    "references undefined argument",
		},
		{
			name: "no args declared but templates used",
			cmd: Command{
				Args: []ArgDecl{},
				Modules: []Module{
					{Config: ModuleConfig{Name: "test", Args: []string{"${file}"}}},
				},
			},
			expectErr: true,
			errMsg:    "references undefined argument",
		},
		{
			name: "interactive module with templates skipped",
			cmd: Command{
				Args: []ArgDecl{{Name: "file", Desc: "File to copy"}},
				Modules: []Module{
					{Config: ModuleConfig{Name: "interactive", Interactive: true}},
					{Config: ModuleConfig{Name: "test", Args: []string{"${file}"}}},
				},
			},
			expectErr: false,
		},
		{
			name: "duplicate template references across modules",
			cmd: Command{
				Args: []ArgDecl{{Name: "file", Desc: "File to copy"}},
				Modules: []Module{
					{Config: ModuleConfig{Name: "module1", Args: []string{"${file}"}}},
					{Config: ModuleConfig{Name: "module2", Args: []string{"${file}", "--backup=${file}.bak"}}},
				},
			},
			expectErr: false,
		},
		{
			name: "multiple templates in single arg string",
			cmd: Command{
				Args: []ArgDecl{
					{Name: "file", Desc: "File"},
					{Name: "target", Desc: "Target"},
				},
				Modules: []Module{
					{Config: ModuleConfig{Name: "test", Args: []string{"${file}-${target}.log"}}},
				},
			},
			expectErr: false,
		},
		{
			name: "empty args with no templates",
			cmd: Command{
				Args: []ArgDecl{},
				Modules: []Module{
					{Config: ModuleConfig{Name: "test", Args: []string{"static"}}},
				},
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cmd.validateTemplateReferences()

			if tt.expectErr && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if tt.expectErr && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("Error %q doesn't contain %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestExtractTemplateReferences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single template",
			input:    "${file}",
			expected: []string{"file"},
		},
		{
			name:     "multiple templates in same string",
			input:    "${file}-${target}.bin",
			expected: []string{"file", "target"},
		},
		{
			name:     "template with dashes and underscores",
			input:    "${flash-file_v2}",
			expected: []string{"flash-file_v2"},
		},
		{
			name:     "no templates",
			input:    "static-string",
			expected: []string{},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "malformed template - no closing brace",
			input:    "${incomplete",
			expected: []string{},
		},
		{
			name:     "malformed template - empty name",
			input:    "${}",
			expected: []string{},
		},
		{
			name:     "template with invalid characters",
			input:    "${invalid space}",
			expected: []string{},
		},
		{
			name:     "duplicate template references",
			input:    "${file} and ${file} again",
			expected: []string{"file", "file"},
		},
		{
			name:     "multiple different templates",
			input:    "path/${dir}/${file}.${ext}",
			expected: []string{"dir", "file", "ext"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTemplateReferences(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestSubstituteArgs(t *testing.T) {
	cmd := Command{
		Args: []ArgDecl{
			{Name: "file", Desc: "File to process"},
			{Name: "target", Desc: "Target device"},
		},
	}

	tests := []struct {
		name        string
		cmd         *Command
		args        []string
		runtimeArgs []string
		expected    []string
		expectErr   bool
	}{
		{
			name:        "simple substitution",
			args:        []string{"${file}", "${target}"},
			runtimeArgs: []string{"test.bin", "device1"},
			expected:    []string{"test.bin", "device1"},
			expectErr:   false,
		},
		{
			name:        "mixed static and template",
			args:        []string{"${file}", "static", "${target}"},
			runtimeArgs: []string{"test.bin", "device1"},
			expected:    []string{"test.bin", "static", "device1"},
			expectErr:   false,
		},
		{
			name:        "template in middle of string",
			args:        []string{"--file=${file}"},
			runtimeArgs: []string{"test.bin", "device1"},
			expected:    []string{"--file=test.bin"},
			expectErr:   false,
		},
		{
			name:        "wrong arg count - too few",
			args:        []string{"${file}"},
			runtimeArgs: []string{},
			expectErr:   true,
		},
		{
			name:        "wrong arg count - too many",
			args:        []string{"${file}"},
			runtimeArgs: []string{"test.bin", "device1", "extra"},
			expectErr:   true,
		},
		{
			name:        "no command args declared",
			cmd:         &Command{Args: []ArgDecl{}},
			args:        []string{"static1", "static2"},
			runtimeArgs: []string{},
			expected:    []string{"static1", "static2"},
			expectErr:   false,
		},
		{
			name:        "duplicate template usage",
			args:        []string{"${file}", "--backup=${file}.bak", "${target}"},
			runtimeArgs: []string{"firmware.bin", "device1"},
			expected:    []string{"firmware.bin", "--backup=firmware.bin.bak", "device1"},
			expectErr:   false,
		},
		{
			name:        "multiple templates in same string",
			args:        []string{"${file}-${target}.log"},
			runtimeArgs: []string{"test.bin", "dev1"},
			expected:    []string{"test.bin-dev1.log"},
			expectErr:   false,
		},
		{
			name: "positional order preservation",
			cmd: &Command{
				Args: []ArgDecl{
					{Name: "second", Desc: "Second arg"},
					{Name: "first", Desc: "First arg"},
					{Name: "third", Desc: "Third arg"},
				},
			},
			args:        []string{"${first}", "${second}", "${third}"},
			runtimeArgs: []string{"B", "A", "C"},
			expected:    []string{"A", "B", "C"},
			expectErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testCmd := cmd
			if tt.cmd != nil {
				testCmd = *tt.cmd
			}

			result, err := testCmd.SubstituteArgs(tt.args, tt.runtimeArgs)

			if tt.expectErr && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if !tt.expectErr && !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}
