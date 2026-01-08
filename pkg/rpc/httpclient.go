// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package rpc provides HTTP client utilities for RPC communication.
package rpc

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/http2"
)

const (
	// HTTP transport timeout configurations.
	responseHeaderTimeout = 10 * time.Second
	idleConnTimeout       = 90 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	expectContinueTimeout = 1 * time.Second
)

// NewClient creates an HTTP client for RPC communication.
// If insecure is true, it returns an h2c (HTTP/2 without TLS) client with "http" scheme.
// Otherwise, it returns a TLS-enabled HTTP/2 client with proper timeouts and "https" scheme.
// Returns the HTTP client and the URL scheme to use.
func NewClient(insecure bool) (*http.Client, string) {
	if insecure {
		return newInsecureClient(), "http"
	}

	return newTLSClient(), "https"
}

// newTLSClient creates an HTTP client configured for TLS with HTTP/2.
func newTLSClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				//nolint:gosec // User controls server trust; InsecureSkipVerify appropriate for test environments
				InsecureSkipVerify: true,
				MinVersion:         tls.VersionTLS13,
			},
			ResponseHeaderTimeout: responseHeaderTimeout,
			IdleConnTimeout:       idleConnTimeout,
			TLSHandshakeTimeout:   tlsHandshakeTimeout,
			ExpectContinueTimeout: expectContinueTimeout,
		},
	}
}

// newInsecureClient creates an HTTP client for h2c (HTTP/2 without TLS).
func newInsecureClient() *http.Client {
	return &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				//nolint:noctx
				return net.Dial(network, addr)
			},
		},
	}
}
