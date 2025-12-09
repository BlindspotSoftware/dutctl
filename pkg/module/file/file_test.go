// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package file

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestInit(t *testing.T) {
	tests := []struct {
		name     string
		file     File
		wantFile *File
		wantErr  bool
		errMsg   string
	}{
		// Valid permission tests
		{
			name: "valid permission 0644",
			file: File{
				Operation:  "upload",
				Permission: "0644",
			},
			wantFile: &File{
				Operation:  "upload",
				Permission: "0644",
			},
			wantErr: false,
		},
		{
			name: "valid permission 0755",
			file: File{
				Operation:  "upload",
				Permission: "0755",
			},
			wantFile: &File{
				Operation:  "upload",
				Permission: "0755",
			},
			wantErr: false,
		},
		{
			name: "valid permission 0600",
			file: File{
				Operation:  "upload",
				Permission: "0600",
			},
			wantFile: &File{
				Operation:  "upload",
				Permission: "0600",
			},
			wantErr: false,
		},
		{
			name: "empty permission sets default",
			file: File{
				Operation:  "upload",
				Permission: "",
			},
			wantFile: &File{
				Operation:  "upload",
				Permission: "0644",
			},
			wantErr: false,
		},
		{
			name: "invalid permission - not octal",
			file: File{
				Operation:  "upload",
				Permission: "0999",
			},
			wantErr: true,
			errMsg:  "invalid permission",
		},
		{
			name: "invalid permission - decimal",
			file: File{
				Operation:  "upload",
				Permission: "44",
			},
			wantErr: true,
			errMsg:  "invalid permission",
		},
		{
			name: "invalid permission - text",
			file: File{
				Operation:  "upload",
				Permission: "rwxr-xr-x",
			},
			wantErr: true,
			errMsg:  "invalid permission",
		},
		{
			name: "invalid permission - empty quotes",
			file: File{
				Operation:  "upload",
				Permission: "''",
			},
			wantErr: true,
			errMsg:  "invalid permission",
		},

		// Valid operation tests
		{
			name: "valid operation upload",
			file: File{
				Operation: "upload",
			},
			wantFile: &File{
				Operation:  "upload",
				Permission: "0644",
			},
			wantErr: false,
		},
		{
			name: "valid operation download",
			file: File{
				Operation: "download",
			},
			wantFile: &File{
				Operation:  "download",
				Permission: "0644",
			},
			wantErr: false,
		},
		{
			name: "missing operation",
			file: File{
				Operation: "",
			},
			wantErr: true,
			errMsg:  "operation must be set in config",
		},
		{
			name: "invalid operation",
			file: File{
				Operation: "copy",
			},
			wantErr: true,
			errMsg:  "invalid operation",
		},
		{
			name: "invalid operation uppercase",
			file: File{
				Operation: "UPLOAD",
			},
			wantErr: true,
			errMsg:  "invalid operation",
		},
		{
			name: "invalid operation mixed case",
			file: File{
				Operation: "Upload",
			},
			wantErr: true,
			errMsg:  "invalid operation",
		},
		{
			name: "invalid operation with spaces",
			file: File{
				Operation: " upload ",
			},
			wantErr: true,
			errMsg:  "invalid operation",
		},

		// Combined config tests
		{
			name: "fully valid upload config",
			file: File{
				Operation:  "upload",
				Permission: "0644",
			},
			wantFile: &File{
				Operation:  "upload",
				Permission: "0644",
			},
			wantErr: false,
		},
		{
			name: "fully valid download config",
			file: File{
				Operation:   "download",
				Destination: "/tmp/file.bin",
			},
			wantFile: &File{
				Operation:   "download",
				Destination: "/tmp/file.bin",
				Permission:  "0644",
			},
			wantErr: false,
		},
		{
			name: "invalid permission and missing operation",
			file: File{
				Operation:  "",
				Permission: "invalid",
			},
			wantErr: true,
			errMsg:  "invalid permission",
		},
		{
			name: "missing operation only",
			file: File{
				Permission: "0755",
			},
			wantErr: true,
			errMsg:  "operation must be set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.file.Init()
			if (err != nil) != tt.wantErr {
				t.Errorf("Init() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("Init() error = %q, want error containing %q", err.Error(), tt.errMsg)
			}

			// Check file state if wantFile is specified
			if !tt.wantErr && tt.wantFile != nil {
				if diff := cmp.Diff(tt.wantFile, &tt.file, cmpopts.IgnoreUnexported(File{})); diff != "" {
					t.Errorf("Init() file mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}
