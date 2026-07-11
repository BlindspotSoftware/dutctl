// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc_test

// This is a deliberate black-box test package (rpc_test): it exercises the
// version interceptors only through the exported API, wired over a real HTTP
// transport exactly as dutctl and dutagent wire them. A separate test package
// is the exception rather than the rule here — it is used because the value
// under test IS the client<->agent handshake, which only has meaning across the
// transport boundary. White-box tests of the internals live in version_test.go
// (package rpc).

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/internal/rpc"
	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
)

// capturingHandler records the message of every log record, per level.
type capturingHandler struct {
	records map[slog.Level][]string
}

func (h *capturingHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.records[r.Level] = append(h.records[r.Level], r.Message)

	return nil
}

func (h *capturingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(_ string) slog.Handler      { return h }

// capture returns a context carrying a logger that records into h.
func capture() (context.Context, *capturingHandler) {
	h := &capturingHandler{records: make(map[slog.Level][]string)}

	return log.Into(context.Background(), slog.New(h)), h
}

// stubService is a minimal DeviceService handler; only List is used.
type stubService struct {
	dutctlv1connect.UnimplementedDeviceServiceHandler
}

func (stubService) List(
	_ context.Context, _ *connect.Request[pb.ListRequest],
) (*connect.Response[pb.ListResponse], error) {
	return connect.NewResponse(&pb.ListResponse{}), nil
}

// newAgent starts a stub agent wired exactly as dutagent is: its handler
// enforces the client version and advertises agentVersion on every response.
func newAgent(t *testing.T, agentVersion string) string {
	t.Helper()

	mux := http.NewServeMux()
	path, handler := dutctlv1connect.NewDeviceServiceHandler(
		stubService{},
		connect.WithInterceptors(rpc.NewVersionEnforcer(agentVersion)),
	)
	mux.Handle(path, handler)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv.URL
}

// TestVersionHandshake exercises both interceptors together: the client
// advertises its version and reports the agent's; the agent enforces the client
// version.
func TestVersionHandshake(t *testing.T) {
	url := newAgent(t, "1.2.0")

	call := func(clientVersion string) (warns, infos []string, err error) {
		ctx, h := capture()

		client := dutctlv1connect.NewDeviceServiceClient(
			http.DefaultClient, url,
			connect.WithInterceptors(rpc.NewVersionAdvisor(clientVersion)),
		)
		_, err = client.List(ctx, connect.NewRequest(&pb.ListRequest{}))

		return h.records[slog.LevelWarn], h.records[slog.LevelInfo], err
	}

	t.Run("compatible: info only, no warn, no error", func(t *testing.T) {
		warns, infos, err := call("1.2.9") // patch diff only
		if err != nil {
			t.Fatalf("List: unexpected error: %v", err)
		}
		if len(warns) != 0 {
			t.Errorf("unexpected warning: %q", warns)
		}
		if len(infos) == 0 {
			t.Error("expected the observed agent version to be logged at info level")
		}
	})

	t.Run("minor mismatch: client warns, call succeeds", func(t *testing.T) {
		warns, _, err := call("1.5.0")
		if err != nil {
			t.Fatalf("List: unexpected error: %v", err)
		}
		if len(warns) == 0 {
			t.Error("expected a warning for a minor mismatch")
		}
	})

	t.Run("major mismatch: agent rejects with FailedPrecondition", func(t *testing.T) {
		_, _, err := call("2.0.0")
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("code = %v, want FailedPrecondition", connect.CodeOf(err))
		}
	})
}

// TestAgentAdvertisesVersion verifies the agent stamps its version on the
// response even when the client runs no version interceptor.
func TestAgentAdvertisesVersion(t *testing.T) {
	url := newAgent(t, "9.9.9")

	client := dutctlv1connect.NewDeviceServiceClient(http.DefaultClient, url)

	res, err := client.List(context.Background(), connect.NewRequest(&pb.ListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if got := res.Header().Get(rpc.VersionHeader); got != "9.9.9" {
		t.Errorf("%s header = %q, want 9.9.9", rpc.VersionHeader, got)
	}
}
