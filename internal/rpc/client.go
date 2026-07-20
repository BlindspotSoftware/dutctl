// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"fmt"
	"net"
	"net/http"
	"time"

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

// dialTimeout bounds establishing a new TCP connection. It is stream-safe — it
// caps connection setup only, never the lifetime of a streaming RPC — and it is
// the one transport-level bound that matters to the one-shot dutctl CLI: a
// per-RPC context deadline (see cmds/dutctl) already bounds a unary dial, but the
// deadline-less streaming Run relies on this to fail fast on an unreachable agent.
const dialTimeout = 10 * time.Second

// newH2CClient builds the shared HTTP/2-cleartext client used for every RPC
// connection. It is unexported: callers obtain a typed client via NewDeviceClient
// or NewRelayClient rather than the raw transport.
func newH2CClient() *http.Client {
	// Use the HTTP/2 protocol without TLS (h2c).
	transport := &http.Transport{
		// Bound connection establishment only; safe for the streaming Run.
		DialContext: (&net.Dialer{Timeout: dialTimeout}).DialContext,
	}
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetUnencryptedHTTP2(true)

	return &http.Client{
		Transport: transport,
		// No http.Client.Timeout: it bounds the whole exchange including the
		// response body, which would abort the long-lived streaming Run. The same
		// reasoning rules out transport.ResponseHeaderTimeout here — this client is
		// shared with Run, whose server writes response headers lazily (on its
		// first Send), so a slow-to-first-output stream would be killed. The real
		// per-call bound is a context deadline on the unary RPCs (see cmds/dutctl),
		// which connect propagates to the agent as a grpc-timeout header.
		// IdleConnTimeout (daemon-side pool hygiene for the long-lived relay) is
		// deferred to the dutserver work.
	}
}
