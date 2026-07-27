// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"crypto/tls"
	"net/http"
	"testing"
)

// TestNewClientTLS pins the transport properties the TLS client must have: it
// speaks HTTP/2 (gRPC does not work without it, and a custom TLSClientConfig
// disables the automatic upgrade), it accepts the agent's unverifiable
// self-signed certificate, and it floors the version at TLS 1.3.
func TestNewClientTLS(t *testing.T) {
	if got := TLS.scheme(); got != "https" {
		t.Errorf("scheme() = %q, want %q", got, "https")
	}

	transport := transportOf(t, newClient(TLS))

	if !transport.Protocols.HTTP2() {
		t.Error("Protocols.HTTP2() = false, want true - gRPC requires HTTP/2")
	}

	if transport.Protocols.UnencryptedHTTP2() {
		t.Error("Protocols.UnencryptedHTTP2() = true, want false on the TLS transport")
	}

	if transport.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig is nil")
	}

	if got := transport.TLSClientConfig.MinVersion; got != tls.VersionTLS13 {
		t.Errorf("MinVersion = %#x, want TLS 1.3 (%#x)", got, tls.VersionTLS13)
	}

	// The agent serves a self-signed certificate, so verification is off by
	// design - encryption without authentication.
	if !transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify = false, want true for the self-signed agent certificate")
	}

	if transport.TLSHandshakeTimeout == 0 {
		t.Error("TLSHandshakeTimeout is unset, want a bound on the handshake")
	}
}

// TestNewClientInsecure pins the cleartext client: h2c, and no TLS settings at
// all.
func TestNewClientInsecure(t *testing.T) {
	if got := Insecure.scheme(); got != "http" {
		t.Errorf("scheme() = %q, want %q", got, "http")
	}

	transport := transportOf(t, newClient(Insecure))

	if !transport.Protocols.UnencryptedHTTP2() {
		t.Error("Protocols.UnencryptedHTTP2() = false, want true - gRPC requires HTTP/2")
	}

	if transport.Protocols.HTTP2() {
		t.Error("Protocols.HTTP2() = true, want false on the cleartext transport")
	}

	if transport.TLSClientConfig != nil {
		t.Error("TLSClientConfig is set on the cleartext transport")
	}
}

// transportOf returns the client's *http.Transport, asserting the client carries
// no overall timeout: http.Client.Timeout bounds the whole exchange including the
// response body, which would abort the long-lived streaming Run.
func transportOf(t *testing.T, client *http.Client) *http.Transport {
	t.Helper()

	if client == nil {
		t.Fatal("newClient returned nil client")
	}

	if client.Timeout != 0 {
		t.Errorf("Client.Timeout = %v, want 0", client.Timeout)
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", client.Transport)
	}

	return transport
}

// TestURL pins the scheme each Security maps onto the dialled address.
func TestURL(t *testing.T) {
	tests := []struct {
		name string
		addr string
		sec  Security
		want string
	}{
		{name: "TLS", addr: "agent:1024", sec: TLS, want: "https://agent:1024"},
		{name: "insecure", addr: "agent:1024", sec: Insecure, want: "http://agent:1024"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := url(tt.addr, tt.sec); got != tt.want {
				t.Errorf("url(%q, %v) = %q, want %q", tt.addr, tt.sec, got, tt.want)
			}
		})
	}
}
