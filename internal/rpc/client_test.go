// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"net/url"
	"testing"
)

// TestBaseURL verifies that the URL of every address DialAddr returns parses
// and leads back to that address, the zone of an IPv6 address included.
func TestBaseURL(t *testing.T) {
	for _, addr := range []string{"rpi:2024", "192.0.2.1:2024", "[::1]:2024", "[fe80::1%eth0]:2024"} {
		t.Run(addr, func(t *testing.T) {
			u, err := url.Parse(baseURL(addr))
			if err != nil {
				t.Fatalf("baseURL(%q) = %q does not parse: %v", addr, baseURL(addr), err)
			}

			if u.Host != addr {
				t.Errorf("baseURL(%q) has host %q, want %q", addr, u.Host, addr)
			}
		})
	}
}
