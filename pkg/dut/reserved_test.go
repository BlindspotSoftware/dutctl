// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package dut

import "testing"

func TestIsReservedCommandName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"reserved help", "help", true},
		{"plain command", "power", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsReservedCommandName(tt.in); got != tt.want {
				t.Errorf("IsReservedCommandName(%q): want %v, got %v", tt.in, tt.want, got)
			}
		})
	}
}

func TestIsReservedDeviceName(t *testing.T) {
	if IsReservedDeviceName("foo") {
		t.Errorf("plain device name should not be reserved")
	}

	if !IsReservedDeviceName("help") {
		t.Errorf("help should be reserved as a device keyword")
	}
}
