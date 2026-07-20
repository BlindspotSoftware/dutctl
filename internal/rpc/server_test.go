// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestServeDrainsInFlightRequest guards the load-bearing graceful-drain behaviour:
// when ctx is cancelled with a request in flight, serve waits for that request to
// finish (rather than cutting it) before returning nil. It fails if the deliberate
// context.WithoutCancel severing is dropped — inheriting ctx's already-fired
// cancellation would make Shutdown return immediately and abort the request — or if
// Shutdown is swapped for Close (which cuts active connections).
func TestServeDrainsInFlightRequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered) // the request is now in flight
		<-release      // block until the test releases it
		w.WriteHeader(http.StatusOK)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	served := make(chan error, 1)

	go func() { served <- serve(ctx, ln, handler) }()

	reqDone := make(chan error, 1)

	go func() {
		client := &http.Client{Timeout: 5 * time.Second}

		resp, err := client.Get("http://" + ln.Addr().String() + "/")
		if err != nil {
			reqDone <- err

			return
		}

		resp.Body.Close()
		reqDone <- nil
	}()

	<-entered // request is in flight inside the handler
	cancel()  // trigger graceful shutdown mid-request

	// serve must keep draining — it must NOT return while the request is unfinished.
	select {
	case err := <-served:
		t.Fatalf("serve returned before the in-flight request finished (not draining): %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	close(release) // let the handler complete

	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serve returned an error after draining: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after the in-flight request completed")
	}

	if err := <-reqDone; err != nil {
		t.Errorf("in-flight request did not complete (it was cut, not drained): %v", err)
	}
}

// TestServeReturnsOnCancel verifies serve returns promptly (nil) when ctx is
// cancelled with nothing in flight.
func TestServeReturnsOnCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: serve should shut down and return

	served := make(chan error, 1)

	go func() { served <- serve(ctx, ln, http.NewServeMux()) }()

	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("serve returned an error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after cancellation")
	}
}
