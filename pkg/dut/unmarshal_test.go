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
