// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssh

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
)

// TestRunEndsOnCancelDuringHandshake checks that a cancelled Run returns while
// the host has accepted the connection but never answers the SSH handshake.
func TestRunEndsOnCancelDuringHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()

	s := &SSH{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, Password: "secret"}
	if err := s.Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, &mock.Session{}, "true") }()

	c := <-accepted
	defer c.Close()

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run returned nil after cancel, want an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
