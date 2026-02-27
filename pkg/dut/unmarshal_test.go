// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dut

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	// Register dummy modules so module.New() succeeds in tests.
	_ "github.com/BlindspotSoftware/dutctl/pkg/module/dummy"
)

// TestDevlistUnmarshalYAML tests that YAML parse errors include device name,
// command name, and a line reference.
func TestDevlistUnmarshalYAML(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantErr     bool
		errContains []string
	}{
		{
			name: "valid single device and command",
			input: `
device1:
  desc: "test device"
  cmds:
    cmd1:
      desc: "ok"
      uses:
        - module: dummy-repeat
`,
			wantErr: false,
		},
		{
			name: "command with no modules",
			input: `
device1:
  desc: "test"
  cmds:
    bad-cmd:
      desc: "no modules"
      uses: []
`,
			wantErr:     true,
			errContains: []string{`device "device1"`, `command "bad-cmd"`, "yaml: line"},
		},
		{
			name: "command with multiple main modules",
			input: `
device1:
  desc: "test"
  cmds:
    bad-cmd:
      desc: "multiple mains"
      uses:
        - module: dummy-repeat
          main: true
        - module: dummy-repeat
          main: true
`,
			wantErr:     true,
			errContains: []string{`device "device1"`, `command "bad-cmd"`, "yaml: line"},
		},
		{
			name: "main module with args set",
			input: `
device1:
  desc: "test"
  cmds:
    bad-cmd:
      desc: "main with args"
      uses:
        - module: dummy-repeat
          main: true
          args:
            - "some-arg"
`,
			wantErr:     true,
			errContains: []string{`device "device1"`, `command "bad-cmd"`, "yaml: line"},
		},
		{
			name: "unknown module name",
			input: `
device1:
  desc: "test"
  cmds:
    bad-cmd:
      desc: "unknown module"
      uses:
        - module: does-not-exist
`,
			wantErr:     true,
			errContains: []string{`device "device1"`, `command "bad-cmd"`},
		},
		{
			name: "error references correct device among multiple",
			input: `
device1:
  desc: "good device"
  cmds:
    cmd1:
      desc: "ok"
      uses:
        - module: dummy-repeat
device2:
  desc: "bad device"
  cmds:
    bad-cmd:
      uses: []
`,
			wantErr:     true,
			errContains: []string{`device "device2"`, `command "bad-cmd"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var devlist Devlist

			err := yaml.Unmarshal([]byte(tt.input), &devlist)

			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v, got err=%v", tt.wantErr, err)
			}

			if err != nil {
				for _, sub := range tt.errContains {
					if !strings.Contains(err.Error(), sub) {
						t.Errorf("error %q does not contain %q", err.Error(), sub)
					}
				}
			}
		})
	}
}

// TestDeviceUnmarshalYAML tests that command-level errors include the command name.
func TestDeviceUnmarshalYAML(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantErr     bool
		errContains []string
	}{
		{
			name: "valid device",
			input: `
desc: "ok"
cmds:
  cmd1:
    uses:
      - module: dummy-repeat
`,
			wantErr: false,
		},
		{
			name: "device with no cmds key",
			input: `
desc: "ok"
`,
			wantErr: false,
		},
		{
			name: "bad command annotated with command name",
			input: `
desc: "ok"
cmds:
  my-cmd:
    uses: []
`,
			wantErr:     true,
			errContains: []string{`command "my-cmd"`, "yaml: line"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dev Device

			err := yaml.Unmarshal([]byte(tt.input), &dev)

			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v, got err=%v", tt.wantErr, err)
			}

			if err != nil {
				for _, sub := range tt.errContains {
					if !strings.Contains(err.Error(), sub) {
						t.Errorf("error %q does not contain %q", err.Error(), sub)
					}
				}
			}
		})
	}
}

// TestCommandUnmarshalYAML tests that validation errors from Command include a line number.
func TestCommandUnmarshalYAML(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantErr     bool
		errContains []string
	}{
		{
			name: "valid single module (implicitly main)",
			input: `
desc: "ok"
uses:
  - module: dummy-repeat
`,
			wantErr: false,
		},
		{
			name: "valid explicit main",
			input: `
desc: "ok"
uses:
  - module: dummy-repeat
    main: true
  - module: dummy-repeat
`,
			wantErr: false,
		},
		{
			name: "no modules",
			input: `
desc: "empty"
uses: []
`,
			wantErr:     true,
			errContains: []string{"yaml: line", "command must have at least one module"},
		},
		{
			name: "multiple main modules",
			input: `
desc: "multi-main"
uses:
  - module: dummy-repeat
    main: true
  - module: dummy-repeat
    main: true
`,
			wantErr:     true,
			errContains: []string{"yaml: line", "command must have exactly one main module"},
		},
		{
			name: "main module with args",
			input: `
desc: "main-with-args"
uses:
  - module: dummy-repeat
    main: true
    args:
      - "foo"
`,
			wantErr:     true,
			errContains: []string{"yaml: line", "main module should not have args"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cmd Command

			err := yaml.Unmarshal([]byte(tt.input), &cmd)

			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v, got err=%v", tt.wantErr, err)
			}

			if err != nil {
				for _, sub := range tt.errContains {
					if !strings.Contains(err.Error(), sub) {
						t.Errorf("error %q does not contain %q", err.Error(), sub)
					}
				}
			}
		})
	}
}
