// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"golang.org/x/mod/semver"

	"github.com/BlindspotSoftware/dutctl/internal/buildinfo"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// TestRegisterAgentAddr verifies that Register stores an agent's address with
// the default port if it names none, and rejects one without a host, at which
// the server could not reach the agent.
func TestRegisterAgentAddr(t *testing.T) {
	tests := []struct {
		addr     string
		want     string
		wantCode connect.Code
	}{
		{addr: "agent:2025", want: "agent:2025"},
		{addr: "agent", want: "agent:2024"},
		{addr: ":2024", wantCode: connect.CodeInvalidArgument},
		{addr: "", wantCode: connect.CodeInvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			s := &rpcService{agents: make(map[string]*agent)}
			req := connect.NewRequest(&pb.RegisterRequest{Address: tt.addr, Devices: []string{"dev"}})

			_, err := s.Register(context.Background(), req)
			if tt.wantCode != 0 {
				if got := connect.CodeOf(err); got != tt.wantCode {
					t.Fatalf("Register(%q) code = %v, want %v", tt.addr, got, tt.wantCode)
				}

				return
			}

			if err != nil {
				t.Fatalf("Register(%q) = %v, want nil", tt.addr, err)
			}

			if got := s.agents["dev"].address; got != tt.want {
				t.Errorf("registered address = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCheckMajorMismatch verifies that a major dutctl version gap is reported as a
// CodeFailedPrecondition error, and that a compatible, empty, or unparsable peer
// is accepted.
func TestCheckMajorMismatch(t *testing.T) {
	// Derive the peer versions from the build version's major so a release-please
	// major bump (which rewrites buildinfo.Version) cannot invert these cases.
	self := buildinfo.Version
	if !strings.HasPrefix(self, "v") {
		self = "v" + self
	}

	major, err := strconv.Atoi(strings.TrimPrefix(semver.Major(self), "v"))
	if err != nil {
		t.Fatalf("cannot parse major from buildinfo.Version %q: %v", buildinfo.Version, err)
	}

	sameMajor := fmt.Sprintf("%d.999.0", major)
	otherMajor := fmt.Sprintf("%d.0.0", major+1)

	tests := []struct {
		name    string
		peer    string
		wantErr bool
	}{
		{"same version", buildinfo.Version, false},
		{"same major, higher minor", sameMajor, false},
		{"major mismatch", otherMajor, true},
		{"empty peer tolerated", "", false},
		{"unparsable peer tolerated", "not-a-version", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkMajorMismatch(tt.peer)

			switch {
			case tt.wantErr && err == nil:
				t.Fatalf("checkMajorMismatch(%q) = nil, want error", tt.peer)
			case tt.wantErr:
				if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
					t.Errorf("code = %v, want FailedPrecondition", got)
				}
			case err != nil:
				t.Errorf("checkMajorMismatch(%q) = %v, want nil", tt.peer, err)
			}
		})
	}
}
