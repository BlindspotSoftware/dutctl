// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import "testing"

func TestDialAddr(t *testing.T) {
	tests := []struct {
		addr    string
		want    string
		wantErr bool
	}{
		{addr: "rpi:2025", want: "rpi:2025"},
		{addr: "rpi", want: "rpi:2024"},
		{addr: "rpi:", want: "rpi:2024"},
		{addr: "192.0.2.1", want: "192.0.2.1:2024"},
		{addr: "[::1]:2025", want: "[::1]:2025"},
		{addr: "[::1]", want: "[::1]:2024"},
		{addr: "::1", want: "[::1]:2024"},
		{addr: "fe80::1%eth0", want: "[fe80::1%eth0]:2024"},
		{addr: ":2024", wantErr: true},
		{addr: "", wantErr: true},
		{addr: "[]", wantErr: true},
		{addr: "a:b:c", wantErr: true},
		{addr: "[::1", wantErr: true},
		{addr: "rpi]", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got, err := DialAddr(tt.addr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DialAddr(%q) error = %v, want error: %t", tt.addr, err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("DialAddr(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestListenAddr(t *testing.T) {
	tests := []struct {
		addr    string
		want    string
		wantErr bool
	}{
		{addr: "localhost:2025", want: "localhost:2025"},
		{addr: "localhost:0", want: "localhost:0"}, // a random port, asked for
		{addr: "localhost", want: "localhost:2024"},
		{addr: "localhost:", want: "localhost:2024"},
		{addr: "0.0.0.0", want: "0.0.0.0:2024"},
		{addr: ":2025", want: ":2025"},
		{addr: ":", want: ":2024"},
		{addr: "::", want: "[::]:2024"},
		{addr: "", wantErr: true},
		{addr: "[]", wantErr: true}, // not all interfaces, as ":" would be
		{addr: "a:b:c", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got, err := ListenAddr(tt.addr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ListenAddr(%q) error = %v, want error: %t", tt.addr, err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("ListenAddr(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}
