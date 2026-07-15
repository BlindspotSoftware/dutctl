// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
)

// TestUserFacingError verifies error rendering: a non-connect error passes through
// unchanged, a connect error drops its "code:" prefix (via Message), and
// CodeUnavailable gets the friendly "cannot reach" line naming the server address.
func TestUserFacingError(t *testing.T) {
	const addr = "host:1234"

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "non-connect error unchanged",
			err:  errors.New("invalid command line"),
			want: "invalid command line",
		},
		{
			name: "connect error drops code prefix",
			err:  connect.NewError(connect.CodeInvalidArgument, errors.New("bad args")),
			want: "bad args",
		},
		{
			name: "unavailable gets friendly line",
			err:  connect.NewError(connect.CodeUnavailable, errors.New("connection refused")),
			want: "cannot reach dutagent at host:1234 (connection refused)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := userFacingError(tt.err, addr); got != tt.want {
				t.Errorf("userFacingError = %q, want %q", got, tt.want)
			}
		})
	}
}
