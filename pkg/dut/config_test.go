// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dut

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	// Register dummy modules so module.New() succeeds in tests.
	_ "github.com/BlindspotSoftware/dutctl/pkg/module/dummy"
)

// NOTE: Tests that unmarshal through the full dutagent config struct
// (version + devices wrapper) are subject to the dutagend command domain and
// should be covered there.

func loadTestdata(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("failed to read testdata/%s: %v", name, err)
	}

	return data
}

func TestInvalidConfig(t *testing.T) {
	tests := []struct {
		name         string
		file         string
		wantSentinel error    // errors.Is check (nil = skip)
		wantDevice   string   // ConfigError.Device (empty = skip check)
		wantCommand  string   // ConfigError.Command (empty = skip check)
		wantLine     int      // expected ConfigError.Line (0 = skip check)
		errKeywords  []string // fallback: substrings in err.Error() when no sentinel
	}{
		// Command-level validation
		{
			name:         "no_modules",
			file:         "invalid_no_modules.yaml",
			wantSentinel: ErrNoModules,
			wantDevice:   "device1",
			wantCommand:  "status",
			wantLine:     5,
		},
		{
			name:         "missing_uses_key",
			file:         "invalid_missing_uses.yaml",
			wantSentinel: ErrNoModules,
			wantDevice:   "device1",
			wantCommand:  "status",
			wantLine:     5,
		},
		{
			name:         "multiple_main_modules",
			file:         "invalid_multiple_main.yaml",
			wantSentinel: ErrMultipleMainModules,
			wantDevice:   "device1",
			wantCommand:  "status",
			wantLine:     5,
		},
		{
			name:         "no_main_in_multi_module",
			file:         "invalid_no_main_multi.yaml",
			wantSentinel: ErrMultipleMainModules,
			wantDevice:   "device1",
			wantCommand:  "status",
			wantLine:     5,
		},
		{
			name:         "main_module_with_args",
			file:         "invalid_main_with_args.yaml",
			wantSentinel: ErrMainModuleWithArgs,
			wantDevice:   "device1",
			wantCommand:  "status",
			wantLine:     5,
		},
		{
			name:         "main_with_args_explicit_multi",
			file:         "invalid_main_args_multi.yaml",
			wantSentinel: ErrMainModuleWithArgs,
			wantDevice:   "device1",
			wantCommand:  "status",
			wantLine:     5,
		},

		// Module-level errors
		{
			name:         "unknown_module",
			file:         "invalid_unknown_module.yaml",
			wantSentinel: ErrModuleNotFound,
			wantDevice:   "device1",
			wantCommand:  "status",
			wantLine:     7,
		},
		{
			name:         "empty_module_name",
			file:         "invalid_empty_module_name.yaml",
			wantSentinel: ErrModuleNotFound,
			wantDevice:   "device1",
			wantCommand:  "status",
			wantLine:     7,
		},

		// Edge cases (treated as invalid)
		{
			name:         "empty_devices",
			file:         "invalid_empty_devices.yaml",
			wantSentinel: ErrEmptyDevices,
		},
		{
			name:         "device_no_commands",
			file:         "invalid_no_commands.yaml",
			wantSentinel: ErrNoCommands,
			wantDevice:   "device1",
		},

		// YAML type mismatch (sanity check)
		{
			name:        "uses_wrong_type",
			file:        "invalid_uses_wrong_type.yaml",
			errKeywords: []string{"cannot unmarshal"},
		},

		// Context propagation
		{
			name:         "error_references_device",
			file:         "invalid_device_context.yaml",
			wantSentinel: ErrNoModules,
			wantDevice:   "myboard",
			wantCommand:  "flash",
			wantLine:     5,
		},
		{
			name:         "error_references_command",
			file:         "invalid_command_context.yaml",
			wantSentinel: ErrNoModules,
			wantDevice:   "device1",
			wantCommand:  "flash",
			wantLine:     5,
		},
		{
			name:         "second_device_bad",
			file:         "invalid_second_device.yaml",
			wantSentinel: ErrNoModules,
			wantDevice:   "device2",
			wantCommand:  "broken",
			wantLine:     12,
		},

		// Null device value
		{
			name:         "null_device",
			file:         "invalid_null_device.yaml",
			wantSentinel: ErrNoCommands,
			wantDevice:   "device1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := loadTestdata(t, tt.file)

			var devs Devlist

			err := yaml.Unmarshal(data, &devs)
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			t.Logf("error message: %s", err)

			// Sentinel check
			if tt.wantSentinel != nil && !errors.Is(err, tt.wantSentinel) {
				t.Errorf("errors.Is: want %v, got %v", tt.wantSentinel, err)
			}

			// Structured error check
			var configErr *ConfigError
			if errors.As(err, &configErr) {
				if tt.wantDevice != "" && configErr.Device != tt.wantDevice {
					t.Errorf("ConfigError.Device: want %q, got %q", tt.wantDevice, configErr.Device)
				}

				if tt.wantCommand != "" && configErr.Command != tt.wantCommand {
					t.Errorf("ConfigError.Command: want %q, got %q", tt.wantCommand, configErr.Command)
				}

				if tt.wantLine != 0 && configErr.Line != tt.wantLine {
					t.Errorf("ConfigError.Line: want %d, got %d", tt.wantLine, configErr.Line)
				}
			} else if tt.wantSentinel != nil {
				// If we expected a sentinel, we also expect a ConfigError wrapper
				t.Errorf("expected *ConfigError, got %T: %v", err, err)
			}

			// Keyword fallback (when no sentinel is available)
			for _, kw := range tt.errKeywords {
				if !strings.Contains(err.Error(), kw) {
					t.Errorf("error %q missing keyword %q", err.Error(), kw)
				}
			}
		})
	}
}

func TestValidConfig(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		wantDevs  int
		checkFunc func(t *testing.T, devs Devlist)
	}{
		{
			name:     "single_device_single_command",
			file:     "valid_single.yaml",
			wantDevs: 1,
		},
		{
			name:     "implicit_main",
			file:     "valid_implicit_main.yaml",
			wantDevs: 1,
			checkFunc: func(t *testing.T, devs Devlist) {
				t.Helper()

				cmd := devs["device1"].Cmds["status"]
				if !cmd.Modules[0].Config.Main {
					t.Error("expected single module to be implicitly set as main")
				}
			},
		},
		{
			name:     "explicit_main_with_helper",
			file:     "valid_explicit_main.yaml",
			wantDevs: 1,
			checkFunc: func(t *testing.T, devs Devlist) {
				t.Helper()

				cmd := devs["device1"].Cmds["status"]
				mainCount := 0

				for _, mod := range cmd.Modules {
					if mod.Config.Main {
						mainCount++
					}
				}

				if mainCount != 1 {
					t.Errorf("expected exactly 1 main module, got %d", mainCount)
				}
			},
		},
		{
			name:     "helper_with_args",
			file:     "valid_helper_args.yaml",
			wantDevs: 1,
			checkFunc: func(t *testing.T, devs Devlist) {
				t.Helper()

				cmd := devs["device1"].Cmds["status"]

				for _, mod := range cmd.Modules {
					if mod.Config.Main && len(mod.Config.Args) > 0 {
						t.Error("main module should not have args")
					}

					if !mod.Config.Main && len(mod.Config.Args) == 0 {
						t.Error("expected helper module to have args")
					}
				}
			},
		},
		{
			name:     "multiple_devices",
			file:     "valid_multi_device.yaml",
			wantDevs: 2,
		},
		{
			name:     "multiple_commands",
			file:     "valid_multi_command.yaml",
			wantDevs: 1,
			checkFunc: func(t *testing.T, devs Devlist) {
				t.Helper()

				dev := devs["device1"]
				if len(dev.Cmds) != 2 {
					t.Errorf("expected 2 commands, got %d", len(dev.Cmds))
				}

				if _, ok := dev.Cmds["status"]; !ok {
					t.Error("expected command 'status'")
				}

				if _, ok := dev.Cmds["repeat"]; !ok {
					t.Error("expected command 'repeat'")
				}
			},
		},
		{
			name:     "command_desc",
			file:     "valid_desc.yaml",
			wantDevs: 1,
			checkFunc: func(t *testing.T, devs Devlist) {
				t.Helper()

				cmd := devs["device1"].Cmds["flash"]
				if cmd.Desc != "Flash the firmware" {
					t.Errorf("Desc: want %q, got %q", "Flash the firmware", cmd.Desc)
				}
			},
		},
		{
			name:     "all_dummy_modules",
			file:     "valid_all_dummies.yaml",
			wantDevs: 1,
			checkFunc: func(t *testing.T, devs Devlist) {
				t.Helper()

				cmd := devs["device1"].Cmds["all"]
				if len(cmd.Modules) != 3 {
					t.Errorf("expected 3 modules, got %d", len(cmd.Modules))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := loadTestdata(t, tt.file)

			var devs Devlist

			err := yaml.Unmarshal(data, &devs)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(devs) != tt.wantDevs {
				t.Errorf("device count: want %d, got %d", tt.wantDevs, len(devs))
			}

			if tt.checkFunc != nil {
				tt.checkFunc(t, devs)
			}
		})
	}
}
