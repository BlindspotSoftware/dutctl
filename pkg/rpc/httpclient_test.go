// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc_test

import (
	"crypto/tls"
	"net/http"
	"testing"

	"github.com/BlindspotSoftware/dutctl/pkg/rpc"
	"golang.org/x/net/http2"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name              string
		insecure          bool
		wantScheme        string
		wantTransportType interface{}
	}{
		{
			name:              "insecure returns http and http2.Transport",
			insecure:          true,
			wantScheme:        "http",
			wantTransportType: &http2.Transport{},
		},
		{
			name:              "secure returns https and http.Transport with TLS",
			insecure:          false,
			wantScheme:        "https",
			wantTransportType: &http.Transport{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, scheme := rpc.NewClient(tt.insecure)

			if client == nil {
				t.Fatal("NewClient returned nil client")
			}

			if scheme != tt.wantScheme {
				t.Errorf("scheme = %q, want %q", scheme, tt.wantScheme)
			}

			if client.Transport == nil {
				t.Fatal("Client transport is nil")
			}

			switch tt.wantTransportType.(type) {
			case *http2.Transport:
				if _, ok := client.Transport.(*http2.Transport); !ok {
					t.Errorf("Expected *http2.Transport, got %T", client.Transport)
				}
			case *http.Transport:
				transport, ok := client.Transport.(*http.Transport)
				if !ok {
					t.Fatalf("Expected *http.Transport, got %T", client.Transport)
				}

				if transport.TLSClientConfig == nil {
					t.Fatal("TLS client config is nil")
				}
				if transport.TLSClientConfig.MinVersion != tls.VersionTLS13 {
					t.Errorf("MinVersion = %v, want TLS 1.3", transport.TLSClientConfig.MinVersion)
				}
			}
		})
	}
}
