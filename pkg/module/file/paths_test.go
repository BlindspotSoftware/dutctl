// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package file

import (
	"strings"
	"testing"
)

func TestParsePaths(t *testing.T) {
	tests := []struct {
		name      string
		arg       string
		operation string
		source    string
		dest      string
		wantSrc   string
		wantDest  string
		wantErr   bool
		errMsg    string
	}{
		// Destination configured - arg becomes source
		{
			name:      "destination configured - arg is source",
			arg:       "./firmware.bin",
			operation: "upload",
			dest:      "/tmp/rom.bin",
			wantSrc:   "./firmware.bin",
			wantDest:  "/tmp/rom.bin",
		},
		{
			name:      "destination configured - absolute source path",
			arg:       "/home/user/firmware.bin",
			operation: "upload",
			dest:      "/opt/firmware.bin",
			wantSrc:   "/home/user/firmware.bin",
			wantDest:  "/opt/firmware.bin",
		},
		{
			name:      "destination configured - relative source path",
			arg:       "../firmware.bin",
			operation: "upload",
			dest:      "/var/lib/firmware.bin",
			wantSrc:   "../firmware.bin",
			wantDest:  "/var/lib/firmware.bin",
		},
		{
			name:      "download with destination - user provides source",
			arg:       "/var/log/dut.log",
			operation: "download",
			dest:      "./local/output.bin",
			wantSrc:   "/var/log/dut.log",
			wantDest:  "./local/output.bin",
		},
		{
			name:      "download - absolute source to absolute dest",
			arg:       "/tmp/rom.bin",
			operation: "download",
			dest:      "/home/user/backup.bin",
			wantSrc:   "/tmp/rom.bin",
			wantDest:  "/home/user/backup.bin",
		},

		// Source configured - arg becomes destination
		{
			name:      "source configured - arg is destination",
			arg:       "./user-provided.bin",
			operation: "upload",
			source:    "/configured/source.bin",
			wantSrc:   "/configured/source.bin",
			wantDest:  "user-provided.bin",
		},
		{
			name:      "source configured - relative paths",
			arg:       "./dest.bin",
			operation: "upload",
			source:    "./config/file.bin",
			wantSrc:   "./config/file.bin",
			wantDest:  "dest.bin",
		},
		{
			name:      "source configured - absolute source and relative dest",
			arg:       "./output/result.bin",
			operation: "upload",
			source:    "/var/log/system.log",
			wantSrc:   "/var/log/system.log",
			wantDest:  "output/result.bin",
		},
		{
			name:      "source configured - absolute dest rejected",
			arg:       "/absolute/dest.bin",
			operation: "upload",
			source:    "./config/file.bin",
			wantErr:   true,
			errMsg:    "absolute paths are not allowed",
		},
		{
			name:      "source configured - dest with leading .. rejected",
			arg:       "../parent/dest.bin",
			operation: "upload",
			source:    "./config/file.bin",
			wantErr:   true,
			errMsg:    "paths with leading '..' are not allowed",
		},

		// Both configured
		{
			name:      "both configured - no arg required",
			arg:       "",
			operation: "upload",
			source:    "/configured/source.bin",
			dest:      "/configured/dest.bin",
			wantSrc:   "/configured/source.bin",
			wantDest:  "/configured/dest.bin",
		},
		{
			name:      "both configured - arg provided should error",
			arg:       "./some-file.bin",
			operation: "upload",
			source:    "/configured/source.bin",
			dest:      "/configured/dest.bin",
			wantErr:   true,
			errMsg:    "no path expected when both source and destination are configured",
		},

		// Empty argument errors
		{
			name:      "empty arg - no config - missing source",
			arg:       "",
			operation: "upload",
			wantErr:   true,
			errMsg:    "path required",
		},
		{
			name:      "empty arg - destination configured - missing source",
			arg:       "",
			operation: "upload",
			dest:      "/tmp/file.bin",
			wantErr:   true,
			errMsg:    "path required",
		},
		{
			name:      "empty arg - source configured - missing destination",
			arg:       "",
			operation: "upload",
			source:    "/configured/source.bin",
			wantErr:   true,
			errMsg:    "missing argument: path required",
		},
		{
			name:      "empty arg - download with destination - missing source",
			arg:       "",
			operation: "download",
			dest:      "/tmp/file.bin",
			wantErr:   true,
			errMsg:    "missing argument: path required",
		},
		{
			name:      "empty arg - download with source - missing destination",
			arg:       "",
			operation: "download",
			source:    "/var/log/system.log",
			wantErr:   true,
			errMsg:    "missing argument: path required",
		},

		// Colon syntax - valid cases
		{
			name:      "upload with colon syntax",
			arg:       "./local/file.bin:remote/file.bin",
			operation: "upload",
			wantSrc:   "local/file.bin",
			wantDest:  "remote/file.bin",
		},
		{
			name:      "download with colon syntax",
			arg:       "remote/log.txt:./local/log.txt",
			operation: "download",
			wantSrc:   "remote/log.txt",
			wantDest:  "local/log.txt",
		},

		// Colon syntax - empty validation
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
			name:      "invalid colon syntax - too many colons",
			arg:       "firmware.bin:remote.bin:extra",
			operation: "upload",
			wantErr:   true,
			errMsg:    "colon syntax requires exactly one colon",
		},

		// Colon syntax - absolute paths rejected
		{
			name:      "colon syntax with absolute source",
			arg:       "/absolute/source.bin:relative/dest.bin",
			operation: "upload",
			wantErr:   true,
			errMsg:    "absolute paths are not allowed",
		},
		{
			name:      "colon syntax with absolute dest",
			arg:       "relative/source.bin:/absolute/dest.bin",
			operation: "upload",
			wantErr:   true,
			errMsg:    "absolute paths are not allowed",
		},
		{
			name:      "colon syntax with both absolute",
			arg:       "/absolute/source.bin:/absolute/dest.bin",
			operation: "upload",
			wantErr:   true,
			errMsg:    "absolute paths are not allowed",
		},

		// Colon syntax - leading .. rejected
		{
			name:      "colon syntax with source leading ..",
			arg:       "../parent/source.bin:relative/dest.bin",
			operation: "upload",
			wantErr:   true,
			errMsg:    "paths with leading '..' are not allowed",
		},
		{
			name:      "colon syntax with dest leading ..",
			arg:       "relative/source.bin:../parent/dest.bin",
			operation: "upload",
			wantErr:   true,
			errMsg:    "paths with leading '..' are not allowed",
		},

		// Colon syntax - mutual exclusivity
		{
			name:      "colon syntax with destination configured",
			arg:       "./firmware.bin:/custom/path.bin",
			operation: "upload",
			dest:      "/tmp/rom.bin",
			wantErr:   true,
			errMsg:    "cannot use colon syntax when destination is configured",
		},
		{
			name:      "download colon syntax with destination configured",
			arg:       "/var/log/dut.log:./local/log.txt",
			operation: "download",
			dest:      "./output.log",
			wantErr:   true,
			errMsg:    "cannot use colon syntax when destination is configured",
		},
		{
			name:      "colon syntax with source configured",
			arg:       "./local.bin:/remote.bin",
			operation: "upload",
			source:    "/configured/source.bin",
			wantErr:   true,
			errMsg:    "cannot use colon syntax when source is configured",
		},
		{
			name:      "colon syntax with both configs",
			arg:       "./local.bin:/remote.bin",
			operation: "upload",
			source:    "/configured/source.bin",
			dest:      "/configured/dest.bin",
			wantErr:   true,
			errMsg:    "no path expected when both source and destination are configured",
		},

		// No config - relative path preservation
		{
			name:      "preserves relative path structure",
			arg:       "./path/to/firmware.bin",
			operation: "upload",
			wantSrc:   "./path/to/firmware.bin",
			wantDest:  "firmware.bin",
		},
		{
			name:      "single filename",
			arg:       "firmware.bin",
			operation: "upload",
			wantSrc:   "firmware.bin",
			wantDest:  "firmware.bin",
		},

		// No config - absolute paths are used
		{
			name:      "absolute path used",
			arg:       "/home/user/file.txt",
			operation: "upload",
			wantSrc:   "/home/user/file.txt",
			wantDest:  "file.txt",
		},
		{
			name:      "absolute path used - upload scenario",
			arg:       "/var/log/dut.log",
			operation: "upload",
			wantSrc:   "/var/log/dut.log",
			wantDest:  "dut.log",
		},

		// No config - path cleaning
		{
			name:      "removes leading ./",
			arg:       "./file.bin",
			operation: "upload",
			wantSrc:   "./file.bin",
			wantDest:  "file.bin",
		},
		{
			name:      "removes redundant separators",
			arg:       "./path//to///file.bin",
			operation: "upload",
			wantSrc:   "./path//to///file.bin",
			wantDest:  "file.bin",
		},
		{
			name:      "cleans path with trailing slash",
			arg:       "./path/to/dir/",
			operation: "upload",
			wantSrc:   "./path/to/dir/",
			wantDest:  "dir",
		},
		{
			name:      "resolves internal .. references",
			arg:       "./dir/../other/./file.bin",
			operation: "upload",
			wantSrc:   "./dir/../other/./file.bin",
			wantDest:  "file.bin",
		},
		{
			name:      "resolves complex .. paths",
			arg:       "path/to/../from/../../file.bin",
			operation: "upload",
			wantSrc:   "path/to/../from/../../file.bin",
			wantDest:  "file.bin",
		},

		// No config - security: parent directory references rejected
		{
			name:      "paths with leading .. rejected",
			arg:       "../parent/file.bin",
			operation: "upload",
			wantSrc:   "../parent/file.bin",
			wantDest:  "file.bin",
		},
		{
			name:      "paths with multiple leading .. rejected",
			arg:       "../../etc/config.bin",
			operation: "upload",
			wantSrc:   "../../etc/config.bin",
			wantDest:  "config.bin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &File{
				Operation:   tt.operation,
				Source:      tt.source,
				Destination: tt.dest,
			}

			var args []string
			if tt.arg != "" {
				args = []string{tt.arg}
			}

			err := f.parsePaths(args)

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
