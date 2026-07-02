// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package buildinfo

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		name       string
		self, peer string
		want       Compatibility
	}{
		{"identical", "1.0.0-alpha.1", "1.0.0-alpha.1", Compatible},
		{"identical with v", "v1.2.3", "v1.2.3", Compatible},
		{"patch differs", "1.0.0", "1.0.1", Compatible},
		{"minor differs", "1.1.0", "1.0.0", Tolerated},
		{"major differs", "2.0.0", "1.9.9", Incompatible},
		{"prerelease differs", "1.0.0-alpha.1", "1.0.0-alpha.5", Tolerated},
		{"self dev", "devel", "1.0.0", Tolerated},
		{"peer empty", "1.0.0", "", Tolerated},
		{"both garbage", "nope", "also-nope", Tolerated},
		{"git describe ahead of tag", "v1.0.0-alpha.1-5-gabc123", "v1.0.0-alpha.1", Tolerated},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CompareVersions(tc.self, tc.peer)
			if got.Level != tc.want {
				t.Errorf("CompareVersions(%q, %q).Level = %v (%q), want %v",
					tc.self, tc.peer, got.Level, got.Reason, tc.want)
			}
			if got.Level != Compatible && got.Reason == "" {
				t.Errorf("CompareVersions(%q, %q) is non-Compatible with an empty Reason", tc.self, tc.peer)
			}
		})
	}
}

func TestCompareVersionsDelta(t *testing.T) {
	if d := CompareVersions("1.2.0", "1.5.0").Delta; d >= 0 {
		t.Errorf("Delta(self older) = %d, want < 0", d)
	}
	if d := CompareVersions("1.5.0", "1.2.0").Delta; d <= 0 {
		t.Errorf("Delta(self newer) = %d, want > 0", d)
	}
}

// TestClientVersionInterceptorEnforce checks the agent-side guard: it rejects an
// incompatible client version, but tolerates a compatible or missing one.
func TestClientVersionInterceptorEnforce(t *testing.T) {
	i := &clientVersionInterceptor{version: "1.0.0"}
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
