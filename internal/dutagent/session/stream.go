// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// Stream abstracts the bidirectional RPC channel used by the dutagent Run RPC.
//
// General Purpose:
//   - Decouples internal processing of bidirectional RPC streams from concrete
//     transport implementation (currently connect.BidiStream).
//   - Enables unit tests to provide lightweight fakes without standing up a real
//     RPC connection.
//   - Provides only the minimal surface (Send / Receive).
type Stream interface {
	Send(msg *pb.RunResponse) error
	Receive() (*pb.RunRequest, error)
}
