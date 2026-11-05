// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc_test

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/log"
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

// addrHandler passes the address of every "rpc service listening" record on.
type addrHandler struct {
	addrs chan string
}

func (h addrHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h addrHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Message != "rpc service listening" {
		return nil
	}

	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "addr" {
			h.addrs <- a.Value.String()
		}

		return true
	})

	return nil
}

func (h addrHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h addrHandler) WithGroup(string) slog.Handler { return h }

// TestListenAndServeLogsBoundAddress verifies that the server logs the address
// it bound, so a port 0 shows as the port the system picked, and that a client
// can connect once the line is logged.
func TestListenAndServeLogsBoundAddress(t *testing.T) {
	addrs := make(chan string, 1)
	ctx, cancel := context.WithCancel(log.Into(context.Background(), slog.New(addrHandler{addrs: addrs})))

	served := make(chan error, 1)

	go func() { served <- rpc.ListenAndServe(ctx, "127.0.0.1:0", http.NewServeMux()) }()

	var addr string

	select {
	case addr = <-addrs:
	case err := <-served:
		t.Fatalf("the server stopped before it logged its address: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the server logged no address")
	}

	if _, port, err := net.SplitHostPort(addr); err != nil || port == "0" {
		t.Fatalf("logged address %q, want the bound host:port", addr)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("connecting to the logged address: %v", err)
	}

	_ = conn.Close()

	cancel()

	if err := <-served; err != nil {
		t.Errorf("graceful stop returned %v", err)
	}
}
