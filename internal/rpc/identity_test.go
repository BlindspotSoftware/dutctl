// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc_test

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/auth"
	"github.com/BlindspotSoftware/dutctl/internal/rpc"
	"github.com/BlindspotSoftware/dutctl/pkg/headers"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// captureIdentity runs the identifier interceptor over a unary request and
// returns the identity it attached to the context.
func captureIdentity(t *testing.T, req connect.AnyRequest) auth.Identity {
	t.Helper()

	var got auth.Identity

	next := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		got, _ = auth.FromContext(ctx)

		return connect.NewResponse(&pb.LockResponse{}), nil
	}

	if _, err := rpc.NewIdentifier().WrapUnary(next)(context.Background(), req); err != nil {
		t.Fatalf("interceptor: %v", err)
	}

	return got
}

func TestIdentifierAttachesHeaderIdentity(t *testing.T) {
	req := connect.NewRequest(&pb.LockRequest{})
	req.Header().Set(headers.User, "alice@host")

	id := captureIdentity(t, req)

	if id.User() != "alice@host" {
		t.Errorf("identity user = %q, want alice@host", id.User())
	}

	if id.IsAnonymous() {
		t.Error("named header identity reports anonymous")
	}
}

func TestIdentifierAnonymousWhenHeaderAbsent(t *testing.T) {
	id := captureIdentity(t, connect.NewRequest(&pb.LockRequest{}))

	if !id.IsAnonymous() {
		t.Error("header-less identity does not report anonymous")
	}

	if !strings.HasPrefix(id.User(), "unknown-") {
		t.Errorf("identity user = %q, want unknown-<rand> prefix", id.User())
	}
}
