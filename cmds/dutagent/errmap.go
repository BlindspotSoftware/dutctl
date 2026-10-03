// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/session"
)

// cancelCode maps a context cancellation error to its connect status code,
// defaulting to CodeCanceled. It is used at every site that converts a cancelled
// Run to a wire status, so cancellation maps to a single code across the RPC.
func cancelCode(err error) connect.Code {
	if errors.Is(err, context.DeadlineExceeded) {
		return connect.CodeDeadlineExceeded
	}

	return connect.CodeCanceled
}

// cancelError maps a run whose ctx is done to the connect error that fails it.
// A run the agent aborted while stopping is CodeAborted wrapping the cause,
// errAbortedByShutdown: the command did not complete, for a reason on the
// agent's side, and Run's summary tells it from a module failure, CodeAborted
// too, by errors.Is. Otherwise the code comes from cancelCode.
func cancelError(ctx context.Context) error {
	cause := context.Cause(ctx)
	if errors.Is(cause, errAbortedByShutdown) {
		return connect.NewError(connect.CodeAborted, fmt.Errorf("module execution aborted: %w", cause))
	}

	return connect.NewError(cancelCode(ctx.Err()), fmt.Errorf("module execution aborted: %v", ctx.Err()))
}

// receiveError classifies an error from the initial stream Receive: a context
// cancellation maps via cancelCode, an already cancellation-coded connect error
// keeps its code, and anything else is CodeAborted (the run was aborted before it
// began).
func receiveError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return connect.NewError(cancelCode(err), err)
	}

	if code := connect.CodeOf(err); code == connect.CodeCanceled || code == connect.CodeDeadlineExceeded {
		return err
	}

	return connect.NewError(connect.CodeAborted, err)
}

// moduleError maps a terminal module error to the connect error that fails the
// Run: a context cancellation (e.g. a module that honors ctx and returns
// ctx.Err()) maps via cancelCode, keeping cancellation single-valued across the
// RPC; anything else is CodeAborted.
func moduleError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return connect.NewError(cancelCode(err), err)
	}

	return connect.NewError(connect.CodeAborted, fmt.Errorf("module failed: %v", err))
}

// brokerError maps a terminal broker error to the connect error that fails the
// Run: a client protocol violation (session.ErrBadFileTransfer) is
// CodeInvalidArgument, an already-typed connect error (e.g. CodeCanceled on
// client disconnect) keeps its code, and anything else is an internal fault.
func brokerError(err error) error {
	var connectErr *connect.Error

	switch {
	case errors.Is(err, session.ErrBadFileTransfer):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.As(err, &connectErr):
		return err
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
