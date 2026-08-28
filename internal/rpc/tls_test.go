// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/BlindspotSoftware/dutctl/internal/tlsutil"
)

// testCert generates a throwaway self-signed key pair in a temp dir, the same
// way an agent does on first start.
func testCert(t *testing.T) tls.Certificate {
	t.Helper()

	dir := t.TempDir()

	cert, _, err := tlsutil.LoadOrGenerateCert(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if err != nil {
		t.Fatalf("generate cert: %v", err)
	}

	return cert
}

// serveTLSOnListener runs serve over TLS on a listener the caller owns, and
// returns a channel carrying serve's eventual return. Handing serve an
// already-bound listener is what server_test.go does for the cleartext case: it
// removes any need to poll for readiness, because the socket is accepting before
// the first request is made.
func serveTLSOnListener(ctx context.Context, listener net.Listener, handler http.Handler, cert tls.Certificate) chan error {
	served := make(chan error, 1)

	go func() { served <- serve(ctx, listener, handler, serverTLSConfig(cert)) }()

	return served
}

// TestTLSRoundTrip drives a real request from the TLS client against a server
// started over TLS, both using a self-signed certificate. It pins the two
// properties the transports must hold together: the client accepts the
// unverifiable certificate, and the connection ends up on HTTP/2 — gRPC
// (connect.WithGRPC) does not work over HTTP/1.1, and a Transport with a custom
// TLSClientConfig does not negotiate h2 unless asked to.
func TestTLSRoundTrip(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Echo the negotiated protocol back so the client can assert on it.
		w.Header().Set("X-Proto", r.Proto)
		w.WriteHeader(http.StatusNoContent)
	})

	served := serveTLSOnListener(ctx, listener, mux, testCert(t))

	client := newTLSClient()

	resp, err := client.Get("https://" + listener.Addr().String() + "/") //nolint:noctx // short-lived test request
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got := resp.Header.Get("X-Proto"); got != "HTTP/2.0" {
		t.Errorf("server saw %q, want HTTP/2.0 - gRPC requires HTTP/2", got)
	}

	if resp.TLS == nil {
		t.Fatal("response carries no TLS state")
	}

	if resp.TLS.Version != tls.VersionTLS13 {
		t.Errorf("negotiated TLS version = %#x, want TLS 1.3 (%#x)", resp.TLS.Version, tls.VersionTLS13)
	}

	// Release the connection before cancelling: Shutdown waits for pooled
	// connections to go idle, so a live one would stall the drain.
	resp.Body.Close()
	client.CloseIdleConnections()
	cancel()

	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("graceful shutdown returned %v", err)
	}
}

// TestTransportMismatch pins the failure mode the Security argument exists to
// prevent: a cleartext client against a TLS server fails at connect time rather
// than hanging or silently downgrading.
func TestTransportMismatch(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	served := serveTLSOnListener(ctx, listener, http.NewServeMux(), testCert(t))

	client := newH2CClient()

	resp, err := client.Get("http://" + listener.Addr().String() + "/") //nolint:noctx // short-lived test request
	if err == nil {
		resp.Body.Close()
		t.Fatal("cleartext client reached the TLS server, want a connect failure")
	}

	cancel()

	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("graceful shutdown returned %v", err)
	}
}
