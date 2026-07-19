// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package keyword

import "testing"

func TestIsReservedDeviceName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{List, true},
		{Version, true},
		// A command-position keyword is a valid device name.
		{Lock, false},
		{Unlock, false},
		{Help, false},
		{"my-board", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsReservedDeviceName(tt.name); got != tt.want {
				t.Errorf("IsReservedDeviceName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestIsReservedCommandName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{Lock, true},
		{Unlock, true},
		{Help, true},
		// A device-position keyword is a valid command name.
		{List, false},
		{Version, false},
		{"power", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsReservedCommandName(tt.name); got != tt.want {
				t.Errorf("IsReservedCommandName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
