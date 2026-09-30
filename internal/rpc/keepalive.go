// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"net/http"
	"time"
)

// keepalive is the HTTP/2 health check both ends of a dutctl connection run.
//
// A connection can die without a FIN or RST ever arriving: a dropped VPN or NAT
// mapping, a pulled cable, a stuck proxy or tunnel. A streaming Run can be quiet
// for minutes, so without a check neither end notices. The client then waits
// for good, and the agent keeps the session, and with it the device and its
// lock. TCP keepalive does not cover this: it only probes the next hop, which a
// proxy or tunnel still answers. An HTTP/2 ping is answered by the peer itself.
//
// After idle without any frame from the peer a ping is sent; if no answer
// arrives within timeout the connection is closed and its streams fail.
type keepalive struct {
	idle    time.Duration
	timeout time.Duration
}

const (
	keepaliveIdle    = 15 * time.Second
	keepaliveTimeout = 10 * time.Second
)

func defaultKeepalive() keepalive {
	return keepalive{idle: keepaliveIdle, timeout: keepaliveTimeout}
}

// http2Config returns the HTTP/2 settings that enable the health check, for
// both http.Transport.HTTP2 and http.Server.HTTP2.
func (k keepalive) http2Config() *http.HTTP2Config {
	return &http.HTTP2Config{SendPingTimeout: k.idle, PingTimeout: k.timeout}
}
