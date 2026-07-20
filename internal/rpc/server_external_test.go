// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc_test

import (
	"context"
	"net"
	"net/http"
	"testing"

	"github.com/BlindspotSoftware/dutctl/internal/rpc"
)

// TestListenAndServeBindFailure verifies that a server which cannot bind returns
// the error rather than reporting a (graceful) nil — the ctx is never cancelled,
// so the caller can tell a serve failure apart from a signal via ctx.Err().
func TestListenAndServeBindFailure(t *testing.T) {
	// Occupy a port so the server's own bind fails with "address already in use".
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to occupy a port: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = rpc.ListenAndServe(ctx, ln.Addr().String(), http.NewServeMux())
	if err == nil {
		t.Fatal("expected a bind error, got nil")
	}

	if ctx.Err() != nil {
		t.Errorf("context should not be cancelled on a serve failure, got %v", ctx.Err())
	}
}
