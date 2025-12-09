// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package file

import (
	"strings"
	"testing"
)

func TestParsePaths_ColonSyntax(t *testing.T) {
	tests := []struct {
		name        string
		arg         string
		operation   string
		defaultDest string
		wantSrc     string
		wantDest    string
		wantErr     bool
		errMsg      string
	}{
		{
			name:      "upload with colon syntax",
			arg:       "./local/file.bin:/remote/file.bin",
			operation: "upload",
			wantSrc:   "./local/file.bin",
			wantDest:  "/remote/file.bin",
			wantErr:   false,
		},
		{
			name:      "download with colon syntax",
			arg:       "/remote/log.txt:./local/log.txt",
			operation: "download",
			wantSrc:   "/remote/log.txt",
			wantDest:  "./local/log.txt",
			wantErr:   false,
		},
		{
			name:      "paths with multiple colons - splits on first",
			arg:       "C:\\local\\file.bin:/remote/file.bin",
			operation: "upload",
			wantSrc:   "C",
			wantDest:  "\\local\\file.bin:/remote/file.bin",
			wantErr:   false,
		},
		{
			name:      "empty source with colon",
			arg:       ":/remote/file.bin",
			operation: "upload",
			wantErr:   true,
			errMsg:    "src path cannot be empty",
		},
		{
			name:      "empty dest with colon",
			arg:       "/remote/file.bin:",
			operation: "upload",
			wantErr:   true,
			errMsg:    "destination path cannot be empty",
		},
		{
			name:      "both empty with colon",
			arg:       ":",
			operation: "upload",
			wantErr:   true,
			errMsg:    "src path cannot be empty",
		},
		{
			name:        "colon syntax with default destination - should error",
			arg:         "./firmware.bin:/custom/path.bin",
			operation:   "upload",
			defaultDest: "/tmp/rom.bin",
			wantErr:     true,
			errMsg:      "cannot use colon syntax when default_destination is set to",
		},
		{
			name:        "download colon syntax with default destination - should error",
			arg:         "/var/log/dut.log:./local/log.txt",
			operation:   "download",
			defaultDest: "./output.log",
			wantErr:     true,
			errMsg:      "cannot use colon syntax when default_destination is set to",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &File{
				Operation:          tt.operation,
				DefaultDestination: tt.defaultDest,
			}

			err := f.parsePaths(tt.arg)

			if (err != nil) != tt.wantErr {
				t.Fatalf("parsePaths() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("parsePaths() error = %q, want error containing %q", err.Error(), tt.errMsg)
				}
				return
			}

			if f.sourcePath != tt.wantSrc {
				t.Errorf("parsePaths() sourcePath = %q, want %q", f.sourcePath, tt.wantSrc)
			}
			if f.destPath != tt.wantDest {
				t.Errorf("parsePaths() destPath = %q, want %q", f.destPath, tt.wantDest)
			}
		})
	}
}

func TestParsePaths_UploadWithDefaultDestination(t *testing.T) {
	tests := []struct {
		name        string
		arg         string
		defaultDest string
		wantSrc     string
		wantDest    string
	}{
		{
			name:        "upload with default destination set",
			arg:         "./firmware.bin",
			defaultDest: "/tmp/rom.bin",
			wantSrc:     "./firmware.bin",
			wantDest:    "/tmp/rom.bin",
		},
		{
			name:        "upload with absolute path source",
			arg:         "/home/user/firmware.bin",
			defaultDest: "/opt/firmware.bin",
			wantSrc:     "/home/user/firmware.bin",
			wantDest:    "/opt/firmware.bin",
		},
		{
			name:        "upload with relative path",
			arg:         "../firmware.bin",
			defaultDest: "/var/lib/firmware.bin",
			wantSrc:     "../firmware.bin",
			wantDest:    "/var/lib/firmware.bin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &File{
				Operation:          "upload",
				DefaultDestination: tt.defaultDest,
			}

			err := f.parsePaths(tt.arg)
			if err != nil {
				t.Fatalf("parsePaths() error = %v, want nil", err)
			}

			if f.sourcePath != tt.wantSrc {
				t.Errorf("parsePaths() sourcePath = %q, want %q", f.sourcePath, tt.wantSrc)
			}
			if f.destPath != tt.wantDest {
				t.Errorf("parsePaths() destPath = %q, want %q", f.destPath, tt.wantDest)
			}
		})
	}
}

func TestParsePaths_UploadWithoutDefaultDestination(t *testing.T) {
	tests := []struct {
		name     string
		arg      string
		wantSrc  string
		wantDest string
	}{
		{
			name:     "upload uses basename when no default",
			arg:      "./path/to/firmware.bin",
			wantSrc:  "./path/to/firmware.bin",
			wantDest: "firmware.bin",
		},
		{
			name:     "upload with absolute path",
			arg:      "/home/user/file.txt",
			wantSrc:  "/home/user/file.txt",
			wantDest: "file.txt",
		},
		{
			name:     "upload with single filename",
			arg:      "firmware.bin",
			wantSrc:  "firmware.bin",
			wantDest: "firmware.bin",
		},
		{
			name:     "upload with trailing slash",
			arg:      "./path/to/dir/",
			wantSrc:  "./path/to/dir/",
			wantDest: "dir",
		},
		{
			name:     "upload with complex path",
			arg:      "../../parent/firmware.bin",
			wantSrc:  "../../parent/firmware.bin",
			wantDest: "firmware.bin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &File{
				Operation:          "upload",
				DefaultDestination: "", // No default destination
			}

			err := f.parsePaths(tt.arg)
			if err != nil {
				t.Fatalf("parsePaths() error = %v, want nil", err)
			}

			if f.sourcePath != tt.wantSrc {
				t.Errorf("parsePaths() sourcePath = %q, want %q", f.sourcePath, tt.wantSrc)
			}
			if f.destPath != tt.wantDest {
				t.Errorf("parsePaths() destPath = %q, want %q", f.destPath, tt.wantDest)
			}
		})
	}
}

func TestParsePaths_DownloadWithDefaultDestination(t *testing.T) {
	tests := []struct {
		name        string
		arg         string
		defaultDest string
		wantSrc     string
		wantDest    string
	}{
		{
			name:        "download with default destination - user provides source",
			arg:         "/var/log/dut.log",
			defaultDest: "./local/output.bin",
			wantSrc:     "/var/log/dut.log",
			wantDest:    "./local/output.bin",
		},
		{
			name:        "download from absolute path to default dest",
			arg:         "/tmp/rom.bin",
			defaultDest: "/home/user/backup.bin",
			wantSrc:     "/tmp/rom.bin",
			wantDest:    "/home/user/backup.bin",
		},
		{
			name:        "download with relative source path",
			arg:         "../logs/app.log",
			defaultDest: "./local-logs/app.log",
			wantSrc:     "../logs/app.log",
			wantDest:    "./local-logs/app.log",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &File{
				Operation:          "download",
				DefaultDestination: tt.defaultDest,
			}

			err := f.parsePaths(tt.arg)
			if err != nil {
				t.Fatalf("parsePaths() error = %v, want nil", err)
			}

			if f.sourcePath != tt.wantSrc {
				t.Errorf("parsePaths() sourcePath = %q, want %q", f.sourcePath, tt.wantSrc)
			}
			if f.destPath != tt.wantDest {
				t.Errorf("parsePaths() destPath = %q, want %q", f.destPath, tt.wantDest)
			}
		})
	}
}

func TestParsePaths_DownloadWithoutDefaultDestination(t *testing.T) {
	tests := []struct {
		name     string
		arg      string
		wantErr  bool
		errMsg   string
		wantSrc  string
		wantDest string
	}{
		{
			name:     "download without default and no colon uses working dir",
			arg:      "/var/log/dut.log",
			wantErr:  false,
			wantSrc:  "/var/log/dut.log",
			wantDest: "dut.log",
		},
		{
			name:     "download with colon syntax - no default needed",
			arg:      "/var/log/dut.log:./local/log.txt",
			wantErr:  false,
			wantSrc:  "/var/log/dut.log",
			wantDest: "./local/log.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &File{
				Operation:          "download",
				DefaultDestination: "", // No default destination
			}

			err := f.parsePaths(tt.arg)

			if (err != nil) != tt.wantErr {
				t.Fatalf("parsePaths() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("parsePaths() error = %q, want error containing %q", err.Error(), tt.errMsg)
				}
			} else {
				// Verify paths
				if f.sourcePath != tt.wantSrc {
					t.Errorf("parsePaths() sourcePath = %q, want %q", f.sourcePath, tt.wantSrc)
				}
				if f.destPath != tt.wantDest {
					t.Errorf("parsePaths() destPath = %q, want %q", f.destPath, tt.wantDest)
				}
			}
		})
	}
}

func TestParsePaths_EmptyArgument(t *testing.T) {
	tests := []struct {
		name      string
		arg       string
		operation string
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "empty argument for upload",
			arg:       "",
			operation: "upload",
			wantErr:   true,
			errMsg:    "path argument cannot be empty",
		},
		{
			name:      "empty argument for download",
			arg:       "",
			operation: "download",
			wantErr:   true,
			errMsg:    "path argument cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &File{
				Operation:          tt.operation,
				DefaultDestination: "/tmp/file.bin",
			}

			err := f.parsePaths(tt.arg)
			if err == nil {
				t.Fatal("parsePaths() expected error for empty argument, got nil")
			}

			if !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("parsePaths() error = %q, want error containing %q", err.Error(), tt.errMsg)
			}
		})
	}
}
