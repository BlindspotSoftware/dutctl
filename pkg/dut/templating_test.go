// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dut

import (
	"reflect"
	"strings"
	"testing"
)

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
			name: "main module with templates skipped",
			cmd: Command{
				Args: []ArgDecl{{Name: "file", Desc: "File to copy"}},
				Modules: []Module{
					{Config: ModuleConfig{Name: "main", Main: true}},
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
