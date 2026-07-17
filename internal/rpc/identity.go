// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/auth"
	"github.com/BlindspotSoftware/dutctl/pkg/headers"
)

// NewIdentifier returns the agent-side connect interceptor that resolves the
// caller's identity from the request and attaches it to the context for
// handlers to read with [auth.FromContext]. It is the one place the identity
// source is wired in, so it can be replaced by an authenticated source (e.g. an
// mTLS peer certificate) without changing the handlers.
func NewIdentifier() connect.Interceptor {
	return identifier{}
}

type identifier struct{}

// identify resolves the caller's identity from the request headers: a named
// identity from [headers.User], or a fresh anonymous one when it is absent.
func identify(header http.Header) auth.Identity {
	if user := header.Get(headers.User); user != "" {
		return auth.Named(user)
	}

	return auth.Anonymous()
}

func (identifier) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return next(auth.NewContext(ctx, identify(req.Header())), req)
	}
}

func (identifier) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (identifier) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		return next(auth.NewContext(ctx, identify(conn.RequestHeader())), conn)
	}
}
