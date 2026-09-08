// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
)

// Security selects the transport a client dials with. It is an explicit argument
// rather than a default because the choice is not observable at the call site
// otherwise, and getting it wrong fails at connect time with an opaque protocol
// error rather than a clear mismatch.
type Security int

const (
	// TLS dials over HTTPS with HTTP/2 negotiated by ALPN. The server certificate
	// is not verified — see newTLSClient. It is the zero value, and every value
	// other than Insecure is treated as TLS, so a forgotten or malformed argument
	// fails closed.
	TLS Security = iota
	// Insecure dials over HTTP/2 cleartext (h2c), with no encryption at all.
	Insecure
)

// NewDeviceClient returns a DeviceService client for the agent (or server) at
// addr, speaking gRPC over HTTP/2 — TLS or cleartext (h2c) as sec selects. Extra
// options — typically connect.WithInterceptors(NewVersionAdvisor(...)) — are
// appended after the mandatory WithGRPC.
func NewDeviceClient(addr string, sec Security, opts ...connect.ClientOption) dutctlv1connect.DeviceServiceClient {
	return dutctlv1connect.NewDeviceServiceClient(newClient(sec), url(addr, sec), clientOptions(opts)...)
}

// NewRelayClient returns a RelayService client for the server at addr, speaking
// gRPC over HTTP/2 — TLS or cleartext (h2c) as sec selects.
//
//nolint:ireturn // returns the connect-generated RelayServiceClient interface by design
func NewRelayClient(addr string, sec Security, opts ...connect.ClientOption) dutctlv1connect.RelayServiceClient {
	return dutctlv1connect.NewRelayServiceClient(newClient(sec), url(addr, sec), clientOptions(opts)...)
}

func url(addr string, sec Security) string { return fmt.Sprintf("%s://%s", sec.scheme(), addr) }

// scheme is the URL scheme matching the transport s selects.
func (s Security) scheme() string {
	switch s {
	case Insecure:
		return "http"
	case TLS:
		return "https"
	default:
		return "https" // Unknown value: fail closed, as documented on TLS.
	}
}

func clientOptions(opts []connect.ClientOption) []connect.ClientOption {
	return append([]connect.ClientOption{connect.WithGRPC()}, opts...)
}

// dialTimeout bounds establishing a new TCP connection. It is stream-safe — it
// caps connection setup only, never the lifetime of a streaming RPC — and it is
// one of the two transport-level bounds that matter to the one-shot dutctl CLI
// (tlsHandshakeTimeout is the other): a per-RPC context deadline (see cmds/dutctl)
// already bounds a unary dial, but the deadline-less streaming Run relies on this
// to fail fast on an unreachable agent.
const dialTimeout = 10 * time.Second

// idleConnTimeout reaps a pooled connection after it has been idle this long. It
// is pool hygiene, not a per-RPC bound: it never touches an active stream. Inert
// for the one-shot dutctl CLI (which exits long before it fires), it matters for
// the long-lived dutserver relay, whose cached per-agent upstreams would otherwise
// keep a dead keep-alive across an agent restart instead of re-dialing fresh.
const idleConnTimeout = 90 * time.Second

// tlsHandshakeTimeout bounds the TLS handshake, so a host that accepts the TCP
// connection but never completes the handshake fails fast instead of hanging the
// deadline-less streaming Run. It is the TLS counterpart to dialTimeout.
const tlsHandshakeTimeout = 10 * time.Second

// newClient builds the shared HTTP client used for every RPC connection over the
// transport sec selects. It is unexported: callers obtain a typed client via
// NewDeviceClient or NewRelayClient rather than the raw transport.
func newClient(sec Security) *http.Client {
	if sec == Insecure {
		return newH2CClient()
	}

	return newTLSClient()
}

// newTLSClient builds the HTTP/2-over-TLS client. It does NOT verify the server
// certificate: the agent typically serves a self-signed certificate it generates
// itself (see internal/tlsutil), so there is usually no CA to chain to.
// Verification is skipped unconditionally, so an operator-supplied CA-issued
// certificate is not checked either. The connection is therefore encrypted but
// not authenticated — it stops passive eavesdropping on the wire, not an active
// man-in-the-middle. Client authentication is a separate concern and is not
// provided here either: any client may connect.
//
// TLS 1.3 is the floor in both directions: both peers are built from this repo,
// so there is no legacy implementation to accommodate, and it pairs with the
// Ed25519 key tlsutil generates.
func newTLSClient() *http.Client {
	transport := &http.Transport{
		// Bound connection establishment only; safe for the streaming Run.
		DialContext: (&net.Dialer{Timeout: dialTimeout}).DialContext,
		// Reap idle pooled connections; never affects an active stream.
		IdleConnTimeout:     idleConnTimeout,
		TLSHandshakeTimeout: tlsHandshakeTimeout,
		TLSClientConfig: &tls.Config{
			//nolint:gosec // self-signed agent certificate; encryption only, see doc comment
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS13,
		},
	}
	// A Transport with a custom TLSClientConfig does not negotiate HTTP/2 on its
	// own; request it explicitly, since gRPC (connect.WithGRPC) needs HTTP/2.
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP2(true)

	return &http.Client{
		Transport: transport,
		// No http.Client.Timeout — see newH2CClient.
	}
}

// newH2CClient builds the HTTP/2-cleartext client used when TLS is disabled.
func newH2CClient() *http.Client {
	// Use the HTTP/2 protocol without TLS (h2c).
	transport := &http.Transport{
		// Bound connection establishment only; safe for the streaming Run.
		DialContext: (&net.Dialer{Timeout: dialTimeout}).DialContext,
		// Reap idle pooled connections; never affects an active stream.
		IdleConnTimeout: idleConnTimeout,
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
	}
}
