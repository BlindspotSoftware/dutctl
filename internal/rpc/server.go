// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"net/http"
	"time"
)

// readHeaderTimeout bounds how long the server waits to read request headers.
const readHeaderTimeout = 10 * time.Second

// ListenAndServe serves h on addr over HTTP/2 cleartext (h2c) with HTTP/1
// upgrade, applying the standard dutctl server settings. It blocks until the
// server stops and always returns a non-nil error. Callers build the handler
// (mux + connect handlers + interceptors) and pass it in.
func ListenAndServe(addr string, h http.Handler) error {
	return newH2CServer(addr, h).ListenAndServe()
}

// newH2CServer builds the h2c *http.Server. It is unexported because
// ListenAndServe is the only intended entry point; returning the *http.Server
// would be the seam for driving a graceful Shutdown.
func newH2CServer(addr string, h http.Handler) *http.Server {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	// Serve HTTP/2 without TLS (h2c), keeping HTTP/1 for upgrade.
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)

	return srv
}
