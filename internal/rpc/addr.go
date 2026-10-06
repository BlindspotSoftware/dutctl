// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// DefaultPort is the port of a dutagent or dutserver address that names none.
const DefaultPort = "2024"

// DialAddr returns addr, the address of a dutagent or dutserver to connect to,
// as host:port, with DefaultPort if addr names no port. It fails if addr names
// no host.
//
// A user-given address goes through DialAddr before NewDeviceClient or
// NewRelayClient: they dial addr as an http:// URL, where a missing port would
// not fail but become 80, the HTTP default.
func DialAddr(addr string) (string, error) {
	host, port, err := splitAddr(addr)
	if err != nil {
		return "", err
	}

	if host == "" {
		return "", fmt.Errorf("%q has no host to connect to", addr)
	}

	return net.JoinHostPort(host, port), nil
}

// ListenAddr returns addr, the address to listen on, as host:port, with
// DefaultPort if addr names no port; net.Listen would pick a random one. An
// empty host listens on all interfaces only where addr has its colon, as in
// ":2024"; an empty addr, or "[]", fails rather than doing so.
func ListenAddr(addr string) (string, error) {
	host, port, err := splitAddr(addr)
	if err != nil {
		return "", err
	}

	return net.JoinHostPort(host, port), nil
}

// splitAddr splits addr into its host and port, which is DefaultPort if addr
// names none. Without a port, an IPv6 address may come with or without its
// brackets.
func splitAddr(addr string) (string, string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// addr names no port, or it is malformed.
		host = addr
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = host[1 : len(host)-1]
		}

		if !validHost(host) {
			return "", "", fmt.Errorf("%q is not in the format address[:port]", addr)
		}

		port = ""
	}

	if port == "" {
		port = DefaultPort
	}

	return host, port, nil
}

// validHost reports whether host, taken from an address without a port, is a
// host name or an IP address. Such an address must name a host, a colon in it
// can only be part of an IPv6 address, and a bracket is left over from a
// malformed address.
func validHost(host string) bool {
	if host == "" || strings.ContainsAny(host, "[]") {
		return false
	}

	if !strings.Contains(host, ":") {
		return true
	}

	_, err := netip.ParseAddr(host)

	return err == nil
}
