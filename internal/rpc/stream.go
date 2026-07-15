// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"connectrpc.com/connect"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// RunStream adapts a connect.BidiStream for the Run RPC to the minimal
// Send/Receive surface the dutagent session consumes. It is returned as a
// concrete type: callers pass it where a session.Stream is expected, which it
// satisfies structurally, so internal/rpc and the session package need not
// import each other.
//
// Send and Receive forward directly to the underlying connect stream and return
// its error verbatim: the connect error code and io.EOF are preserved. This
// pass-through is load-bearing — the agent's session workers branch on
// errors.Is(err, io.EOF) and the RPC handler preserves connect codes — so these
// errors must not be wrapped here.
type RunStream struct {
	inner *connect.BidiStream[pb.RunRequest, pb.RunResponse]
}

// NewRunStream wraps the Run RPC's bidirectional stream.
func NewRunStream(inner *connect.BidiStream[pb.RunRequest, pb.RunResponse]) *RunStream {
	return &RunStream{inner: inner}
}

func (s *RunStream) Send(msg *pb.RunResponse) error   { return s.inner.Send(msg) }
func (s *RunStream) Receive() (*pb.RunRequest, error) { return s.inner.Receive() }
