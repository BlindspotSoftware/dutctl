// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package rpc provides HTTP client utilities for RPC communication.
package rpc

import (
	"crypto/tls"
	"net"
	"net/http"

	"golang.org/x/net/http2"
)

// NewInsecureClient creates an HTTP client for h2c (HTTP/2 without TLS).
func NewInsecureClient() *http.Client {
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
