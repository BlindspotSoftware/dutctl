// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// readHeaderTimeout bounds how long the server waits to read request headers.
const readHeaderTimeout = 10 * time.Second

// shutdownGracePeriod bounds how long a graceful shutdown waits for in-flight
// requests to drain before ListenAndServe returns. A long-lived streaming Run may
// outlast it; the caller's process exit then closes what remains.
const shutdownGracePeriod = 15 * time.Second

// ListenAndServe serves handler on addr over HTTP/2 cleartext (h2c) with HTTP/1 upgrade,
// applying the standard dutctl server settings. It returns immediately if the
// address cannot be bound; otherwise it serves until ctx is cancelled (draining
// in-flight requests) or the server stops on its own — see serve. Callers build
// the handler (mux + connect handlers + interceptors) and pass it in, and classify
// the return via ctx.Err(): a cancelled ctx means a graceful stop, otherwise the
// server failed to serve.
func ListenAndServe(ctx context.Context, addr string, handler http.Handler) error {
	var lc net.ListenConfig

	listener, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	return serve(ctx, listener, handler, nil)
}

// ListenAndServeTLS is ListenAndServe over TLS, serving handler with cert and
// HTTP/2 negotiated by ALPN. Everything else — binding, graceful shutdown, how
// the caller classifies the return — matches ListenAndServe.
//
// The certificate is typically the agent's own self-signed one (see
// internal/tlsutil), so the connection is encrypted but the client cannot
// authenticate the server, and the server does not authenticate the client:
// any client may connect. See newTLSClient for the peer's side of this.
func ListenAndServeTLS(ctx context.Context, addr string, handler http.Handler, cert tls.Certificate) error {
	var lc net.ListenConfig

	listener, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	return serve(ctx, listener, handler, serverTLSConfig(cert))
}

// serverTLSConfig is the TLS configuration the RPC service serves with. TLS 1.3
// is the floor in both directions: both peers are built from this repo, so there
// is no legacy implementation to accommodate, and it pairs with the Ed25519 key
// tlsutil generates. See newTLSClient for the peer's side.
func serverTLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}
}

// serve runs handler on the given listener, over TLS if tlsConfig is non-nil and
// over h2c otherwise. It blocks until ctx is cancelled — then it stops
// accepting and drains in-flight requests (bounded by shutdownGracePeriod) before
// returning — or until the server stops on its own, in which case it returns that
// error. It is unexported: ListenAndServe and ListenAndServeTLS are the entry
// points; serve is factored out so a test can drive a real in-flight request
// against a listener whose address it controls.
func serve(ctx context.Context, listener net.Listener, handler http.Handler, tlsConfig *tls.Config) error {
	srv := newServer(listener.Addr().String(), handler, tlsConfig)

	errCh := make(chan error, 1)

	go func() {
		if tlsConfig != nil {
			// Empty paths: the certificate is already in srv.TLSConfig.
			errCh <- srv.ServeTLS(listener, "", "")

			return
		}

		errCh <- srv.Serve(listener)
	}()

	select {
	case err := <-errCh:
		// The server stopped on its own.
		return err
	case <-ctx.Done():
		// A signal cancelled ctx: stop accepting and drain in-flight requests,
		// bounded by shutdownGracePeriod. The shutdown context is derived from ctx
		// with WithoutCancel, so it keeps ctx's values but not its (already-fired)
		// cancellation — inheriting the cancellation would make it done immediately
		// and skip the drain.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGracePeriod)
		defer cancel()

		err := srv.Shutdown(shutdownCtx)

		<-errCh // reap the http.ErrServerClosed the serve goroutine now sends

		return err
	}
}

// newServer builds the *http.Server — over TLS if tlsConfig is non-nil, h2c
// otherwise. It is unexported: ListenAndServe and ListenAndServeTLS are the only
// intended entry points and drive the server's graceful Shutdown themselves.
func newServer(addr string, handler http.Handler, tlsConfig *tls.Config) *http.Server {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)

	if tlsConfig != nil {
		// Over TLS, HTTP/2 is negotiated by ALPN; HTTP/1 stays for clients that
		// do not offer h2.
		srv.Protocols.SetHTTP2(true)

		return srv
	}

	// Serve HTTP/2 without TLS (h2c), keeping HTTP/1 for upgrade.
	srv.Protocols.SetUnencryptedHTTP2(true)

	return srv
}
