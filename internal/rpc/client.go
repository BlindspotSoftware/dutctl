// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
)

// NewDeviceClient returns a DeviceService client for the agent (or server) at
// addr, speaking gRPC over HTTP/2 cleartext (h2c). Extra options — typically
// connect.WithInterceptors(NewVersionAdvisor(...)) — are appended after the
// mandatory WithGRPC.
func NewDeviceClient(addr string, opts ...connect.ClientOption) dutctlv1connect.DeviceServiceClient {
	return dutctlv1connect.NewDeviceServiceClient(newH2CClient(), url(addr), clientOptions(opts)...)
}

// NewRelayClient returns a RelayService client for the server at addr, speaking
// gRPC over HTTP/2 cleartext (h2c).
//
//nolint:ireturn // returns the connect-generated RelayServiceClient interface by design
func NewRelayClient(addr string, opts ...connect.ClientOption) dutctlv1connect.RelayServiceClient {
	return dutctlv1connect.NewRelayServiceClient(newH2CClient(), url(addr), clientOptions(opts)...)
}

func url(addr string) string { return fmt.Sprintf("http://%s", addr) }

func clientOptions(opts []connect.ClientOption) []connect.ClientOption {
	return append([]connect.ClientOption{connect.WithGRPC()}, opts...)
}

// newH2CClient builds the shared HTTP/2-cleartext client used for every RPC
// connection. It is unexported: callers obtain a typed client via NewDeviceClient
// or NewRelayClient rather than the raw transport.
func newH2CClient() *http.Client {
	// Use the HTTP/2 protocol without TLS (h2c).
	transport := &http.Transport{}
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetUnencryptedHTTP2(true)

	return &http.Client{
		Transport: transport,
		// TODO: Don't forget timeouts! http.Client.Timeout must not be used here:
		// it bounds the entire exchange including the response body, which would
		// abort long-lived streaming RPCs. Instead use per-RPC context deadlines
		// on unary calls and/or transport timeouts (DialContext,
		// TLSHandshakeTimeout, ResponseHeaderTimeout, IdleConnTimeout).
	}
}
