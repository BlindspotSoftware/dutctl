// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"

	"github.com/BlindspotSoftware/dutctl/internal/dutagent/session"
)

// TestCancelCode verifies the cancellation mapping: context.Canceled ->
// CodeCanceled, context.DeadlineExceeded -> CodeDeadlineExceeded, with
// CodeCanceled as the default.
func TestCancelCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want connect.Code
	}{
		{"canceled", context.Canceled, connect.CodeCanceled},
		{"deadline exceeded", context.DeadlineExceeded, connect.CodeDeadlineExceeded},
		{"wrapped canceled", fmt.Errorf("run: %w", context.Canceled), connect.CodeCanceled},
		{"other defaults to canceled", errors.New("boom"), connect.CodeCanceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cancelCode(tt.err); got != tt.want {
				t.Errorf("cancelCode(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestBrokerError locks the broker-error wire-status classification: a client
// protocol violation is CodeInvalidArgument, an already-typed connect error keeps
// its code, and anything else is CodeInternal.
func TestBrokerError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want connect.Code
	}{
		{"bad file transfer", fmt.Errorf("%w: empty file", session.ErrBadFileTransfer), connect.CodeInvalidArgument},
		{"typed connect code preserved", connect.NewError(connect.CodeCanceled, errors.New("client gone")), connect.CodeCanceled},
		{"plain error is internal", errors.New("boom"), connect.CodeInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := connect.CodeOf(brokerError(tt.err)); got != tt.want {
				t.Errorf("brokerError code = %v, want %v", got, tt.want)
			}
		})
	}
}
