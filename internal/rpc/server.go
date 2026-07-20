// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"context"
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

	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	return serve(ctx, ln, handler)
}

// serve runs handler on the listener ln. It blocks until ctx is cancelled — then it stops
// accepting and drains in-flight requests (bounded by shutdownGracePeriod) before
// returning — or until the server stops on its own, in which case it returns that
// error. It is unexported: ListenAndServe is the entry point; serve is factored out
// so a test can drive a real in-flight request against a listener whose address it
// controls.
func serve(ctx context.Context, ln net.Listener, handler http.Handler) error {
	srv := newH2CServer(ln.Addr().String(), handler)

	errCh := make(chan error, 1)

	go func() { errCh <- srv.Serve(ln) }()

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

// newH2CServer builds the h2c *http.Server. It is unexported: ListenAndServe is
// the only intended entry point and drives the server's graceful Shutdown itself.
func newH2CServer(addr string, handler http.Handler) *http.Server {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	// Serve HTTP/2 without TLS (h2c), keeping HTTP/1 for upgrade.
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)

	return srv
}
