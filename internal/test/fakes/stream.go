// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fakes provides lightweight in-memory test doubles for the dutagent
// session interfaces, letting unit tests drive behavior without a real RPC
// transport.
package fakes

import (
	"io"
	"sync"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// FakeStream is a lightweight in-memory implementation of session.Stream.
//
// Behavior:
//   - Receive() dequeues preloaded requests from RecvQueue; if empty returns io.EOF.
//   - Send() appends responses to Sent slice for later inspection.
//   - Errors can be injected via RecvErr / SendErr to drive error branches.
//
// Methods are safe for concurrent use.
type FakeStream struct {
	mu        sync.Mutex
	RecvQueue []*pb.RunRequest
	RecvErr   error

	Sent    []*pb.RunResponse
	SendErr error
}

// Receive returns RecvErr if it is set, otherwise dequeues the next request
// from RecvQueue, or io.EOF once the queue is empty.
func (f *FakeStream) Receive() (*pb.RunRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.RecvErr != nil {
		return nil, f.RecvErr
	}

	if len(f.RecvQueue) == 0 {
		return nil, io.EOF
	}

	req := f.RecvQueue[0]
	f.RecvQueue = f.RecvQueue[1:]

	return req, nil
}

// Send returns SendErr if it is set, otherwise appends r to Sent for later
// inspection.
func (f *FakeStream) Send(r *pb.RunResponse) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.SendErr != nil {
		return f.SendErr
	}

	f.Sent = append(f.Sent, r)

	return nil
}
