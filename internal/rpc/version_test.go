// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

// These are same-package (white-box) tests: they reach the unexported
// versionEnforcer to test its decision logic directly, without a transport.
// This is the default kind of test file in this repo. The black-box tests that
// drive the exported API over a real HTTP transport are the deliberate
// exception and live in version_external_test.go (package rpc_test).

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
)

// TestVersionEnforcerEnforce checks the agent-side guard: it rejects an
// incompatible client version, but tolerates a compatible or missing one.
func TestVersionEnforcerEnforce(t *testing.T) {
	i := &versionEnforcer{version: "1.0.0"}
	ctx := context.Background()

	header := func(v string) http.Header {
		h := http.Header{}
		h.Set(VersionHeader, v)

		return h
	}

	if err := i.enforce(ctx, header("1.0.5")); err != nil {
		t.Errorf("enforce(compatible) = %v, want nil", err)
	}

	if err := i.enforce(ctx, http.Header{}); err != nil {
		t.Errorf("enforce(missing header) = %v, want nil", err)
	}

	err := i.enforce(ctx, header("2.0.0"))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("enforce(incompatible) code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}
