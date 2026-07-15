// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package buildinfo

import "testing"

func TestCvsShortHash(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "unset"},
		{"whitespace only", "   ", "unset"},
		{"shorter than 7", "abc", "abc"},
		{"exactly 7", "abcdef0", "abcdef0"},
		{"full git hash", "1234567890abcdef", "1234567"},
		{"trimmed then sliced", "  1234567890  ", "1234567"},
		{"trimmed then short", "  ab  ", "ab"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cvsShortHash(tt.in)
			if got != tt.want {
				t.Errorf("cvsShortHash(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
