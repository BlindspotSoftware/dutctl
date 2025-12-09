// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package file

import (
	"strings"
	"testing"
)

func TestValidateConfig_Mode(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid mode 0644",
			mode:    "0644",
			wantErr: false,
		},
		{
			name:    "valid mode 0755",
			mode:    "0755",
			wantErr: false,
		},
		{
			name:    "valid mode 0600",
			mode:    "0600",
			wantErr: false,
		},
		{
			name:    "empty mode is valid",
			mode:    "",
			wantErr: false,
		},
		{
			name:    "invalid mode - not octal",
			mode:    "0999",
			wantErr: true,
			errMsg:  "invalid permission",
		},
		{
			name:    "invalid mode - decimal",
			mode:    "644",
			wantErr: true,
			errMsg:  "invalid permission",
		},
		{
			name:    "invalid mode - text",
			mode:    "rwxr-xr-x",
			wantErr: true,
			errMsg:  "invalid permission",
		},
		{
			name:    "invalid mode - empty quotes",
			mode:    "''",
			wantErr: true,
			errMsg:  "invalid permission",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &File{
				Perm:      tt.mode,
				Operation: "upload", // Required field
			}

			err := f.validateConfig()
			if (err != nil) != tt.wantErr {
				t.Errorf("validateConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validateConfig() error = %q, want error containing %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestValidateConfig_Operation(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "valid operation upload",
			operation: "upload",
			wantErr:   false,
		},
		{
			name:      "valid operation download",
			operation: "download",
			wantErr:   false,
		},
		{
			name:      "missing operation",
			operation: "",
			wantErr:   true,
			errMsg:    "operation must be set in config",
		},
		{
			name:      "invalid operation",
			operation: "copy",
			wantErr:   true,
			errMsg:    "invalid operation",
		},
		{
			name:      "invalid operation uppercase",
			operation: "UPLOAD",
			wantErr:   true,
			errMsg:    "invalid operation",
		},
		{
			name:      "invalid operation mixed case",
			operation: "Upload",
			wantErr:   true,
			errMsg:    "invalid operation",
		},
		{
			name:      "invalid operation with spaces",
			operation: " upload ",
			wantErr:   true,
			errMsg:    "invalid operation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &File{
				Operation: tt.operation,
				Perm:      "", // Valid mode
			}

			err := f.validateConfig()
			if (err != nil) != tt.wantErr {
				t.Errorf("validateConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validateConfig() error = %q, want error containing %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestValidateConfig_Combined(t *testing.T) {
	tests := []struct {
		name    string
		file    File
		wantErr bool
		errMsg  string
	}{
		{
			name: "fully valid upload config",
			file: File{
				Operation: "upload",
				Perm:      "0644",
				ForceDir:  true,
				Overwrite: true,
			},
			wantErr: false,
		},
		{
			name: "fully valid download config",
			file: File{
				Operation:          "download",
				Destination: "/tmp/file.bin",
			},
			wantErr: false,
		},
		{
			name: "invalid mode and missing operation",
			file: File{
				Operation: "",
				Perm:      "invalid",
			},
			wantErr: true,
			errMsg:  "invalid permission",
		},
		{
			name: "missing operation only",
			file: File{
				Perm: "0755",
			},
			wantErr: true,
			errMsg:  "operation must be set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.file.validateConfig()
			if (err != nil) != tt.wantErr {
				t.Errorf("validateConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validateConfig() error = %q, want error containing %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestValidateArguments(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid single argument",
			args:    []string{"./firmware.bin"},
			wantErr: false,
		},
		{
			name:    "valid single argument with colon",
			args:    []string{"./source.bin:/dest.bin"},
			wantErr: false,
		},
		{
			name:    "no arguments",
			args:    []string{},
			wantErr: true,
			errMsg:  "missing argument: path required",
		},
		{
			name:    "nil arguments",
			args:    nil,
			wantErr: true,
			errMsg:  "missing argument: path required",
		},
		{
			name:    "two arguments",
			args:    []string{"./firmware.bin", "/tmp/firmware.bin"},
			wantErr: true,
			errMsg:  "invalid argument count: expected 1 arg, got 2",
		},
		{
			name:    "three arguments",
			args:    []string{"upload", "./firmware.bin", "/tmp/firmware.bin"},
			wantErr: true,
			errMsg:  "invalid argument count: expected 1 arg, got 3",
		},
		{
			name:    "empty string argument is valid",
			args:    []string{""},
			wantErr: false, // validateArguments only checks count, not content
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &File{}
			err := f.validateArguments(tt.args)

			if (err != nil) != tt.wantErr {
				t.Errorf("validateArguments() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("validateArguments() error = %q, want error containing %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}
