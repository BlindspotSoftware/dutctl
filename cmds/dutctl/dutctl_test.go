// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package main

import (
	"context"
	"errors"
	"io"
	"testing"

	"connectrpc.com/connect"

	"github.com/BlindspotSoftware/dutctl/internal/output"
	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
)

// fakeDeviceServiceClient is a hand-written test double for
// dutctlv1connect.DeviceServiceClient. Only the unary RPCs are
// implemented; Run returns nil because the streaming path is not
// exercised in these tests.
type fakeDeviceServiceClient struct {
	listDevices []string
	listErr     error
	listCalls   int

	commandsCalls []string

	detailsCalls []detailsCall

	unlockCalls []unlockCall

	// respectCtx makes the unary methods return ctx.Err() when the received
	// context is already done, mimicking how connect aborts a cancelled or
	// expired call.
	respectCtx bool
	// sawDeadline records, per unary call, whether the received context carried a
	// deadline — the per-call timeout the RPC methods are expected to attach.
	sawDeadline []bool
}

// recordCtx notes whether the unary call arrived with a deadline attached.
func (f *fakeDeviceServiceClient) recordCtx(ctx context.Context) {
	_, ok := ctx.Deadline()
	f.sawDeadline = append(f.sawDeadline, ok)
}

type detailsCall struct {
	device, cmd, keyword string
}

type unlockCall struct {
	device string
	force  bool
}

func (f *fakeDeviceServiceClient) List(
	ctx context.Context, _ *connect.Request[pb.ListRequest],
) (*connect.Response[pb.ListResponse], error) {
	f.recordCtx(ctx)

	if f.respectCtx && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	f.listCalls++

	if f.listErr != nil {
		return nil, f.listErr
	}

	devices := make([]*pb.DeviceInfo, 0, len(f.listDevices))
	for _, name := range f.listDevices {
		devices = append(devices, &pb.DeviceInfo{Name: name})
	}

	return connect.NewResponse(&pb.ListResponse{Devices: devices}), nil
}

func (f *fakeDeviceServiceClient) Commands(
	ctx context.Context, req *connect.Request[pb.CommandsRequest],
) (*connect.Response[pb.CommandsResponse], error) {
	f.recordCtx(ctx)

	if f.respectCtx && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	f.commandsCalls = append(f.commandsCalls, req.Msg.GetDevice())

	return connect.NewResponse(&pb.CommandsResponse{}), nil
}

func (f *fakeDeviceServiceClient) Details(
	ctx context.Context, req *connect.Request[pb.DetailsRequest],
) (*connect.Response[pb.DetailsResponse], error) {
	f.recordCtx(ctx)

	if f.respectCtx && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	f.detailsCalls = append(f.detailsCalls, detailsCall{
		device:  req.Msg.GetDevice(),
		cmd:     req.Msg.GetCommand(),
		keyword: req.Msg.GetKeyword(),
	})

	return connect.NewResponse(&pb.DetailsResponse{}), nil
}

func (f *fakeDeviceServiceClient) Run(
	_ context.Context,
) *connect.BidiStreamForClient[pb.RunRequest, pb.RunResponse] {
	return nil
}

func (f *fakeDeviceServiceClient) Lock(
	ctx context.Context, _ *connect.Request[pb.LockRequest],
) (*connect.Response[pb.LockResponse], error) {
	f.recordCtx(ctx)

	if f.respectCtx && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	return connect.NewResponse(&pb.LockResponse{}), nil
}

func (f *fakeDeviceServiceClient) Unlock(
	ctx context.Context, req *connect.Request[pb.UnlockRequest],
) (*connect.Response[pb.UnlockResponse], error) {
	f.recordCtx(ctx)

	if f.respectCtx && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	f.unlockCalls = append(f.unlockCalls, unlockCall{
		device: req.Msg.GetDevice(),
		force:  req.Msg.GetForce(),
	})

	return connect.NewResponse(&pb.UnlockResponse{}), nil
}

// Compile-time assertion that the fake satisfies the interface.
var _ dutctlv1connect.DeviceServiceClient = (*fakeDeviceServiceClient)(nil)

// newTestApp builds an application with a fake RPC client and a
// discarding formatter. Use it to drive dispatch in unit tests.
func newTestApp(t *testing.T, fake *fakeDeviceServiceClient, args ...string) *application {
	t.Helper()

	return &application{
		stdin:     io.NopCloser(nil),
		stdout:    io.Discard,
		stderr:    io.Discard,
		exitFunc:  func(int) {},
		args:      args,
		rpcClient: fake,
		formatter: output.New(output.Config{Stdout: io.Discard, Stderr: io.Discard}),
	}
}

func TestDispatch(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		listDevices  []string
		wantErrIs    error
		wantListHit  int
		wantCmdHits  []string
		wantDetailHi []detailsCall
		wantUnlock   []unlockCall
	}{
		{
			name:        "no args defaults to list",
			args:        nil,
			wantListHit: 1,
		},
		{
			name:        "explicit list",
			args:        []string{"list"},
			wantListHit: 1,
		},
		{
			name:      "explicit list with extra args is invalid",
			args:      []string{"list", "extra"},
			wantErrIs: errInvalidCmdline,
		},
		{
			name:        "single arg lists commands for that device",
			args:        []string{"mydevice"},
			wantCmdHits: []string{"mydevice"},
		},
		{
			name: "device command help calls details",
			args: []string{"mydevice", "power", "help"},
			wantDetailHi: []detailsCall{
				{device: "mydevice", cmd: "power", keyword: "help"},
			},
		},
		{
			name:      "help with trailing arg is invalid",
			args:      []string{"mydevice", "power", "help", "extra"},
			wantErrIs: errInvalidCmdline,
		},
		{
			name: "lock with a duration is accepted",
			args: []string{"mydevice", "lock", "30m"},
		},
		{
			name:      "lock with extra args is invalid",
			args:      []string{"mydevice", "lock", "30m", "junk"},
			wantErrIs: errInvalidCmdline,
		},
		{
			name:       "unlock releases without force",
			args:       []string{"mydevice", "unlock"},
			wantUnlock: []unlockCall{{device: "mydevice", force: false}},
		},
		{
			name:       "unlock force breaks another lock",
			args:       []string{"mydevice", "unlock", "force"},
			wantUnlock: []unlockCall{{device: "mydevice", force: true}},
		},
		{
			name:      "unlock with a dashed force flag is invalid",
			args:      []string{"mydevice", "unlock", "--force"},
			wantErrIs: errInvalidCmdline,
		},
		{
			name:      "unlock with extra args is invalid",
			args:      []string{"mydevice", "unlock", "force", "extra"},
			wantErrIs: errInvalidCmdline,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeDeviceServiceClient{listDevices: tt.listDevices}
			app := newTestApp(t, fake, tt.args...)

			err := app.dispatch()

			if tt.wantErrIs != nil {
				if !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("dispatch error: want %v, got %v", tt.wantErrIs, err)
				}
			} else if err != nil {
				t.Fatalf("dispatch: unexpected error: %v", err)
			}

			if fake.listCalls != tt.wantListHit {
				t.Errorf("List calls: want %d, got %d", tt.wantListHit, fake.listCalls)
			}

			if !equalStrings(fake.commandsCalls, tt.wantCmdHits) {
				t.Errorf("Commands calls: want %v, got %v", tt.wantCmdHits, fake.commandsCalls)
			}

			if !equalDetails(fake.detailsCalls, tt.wantDetailHi) {
				t.Errorf("Details calls: want %v, got %v", tt.wantDetailHi, fake.detailsCalls)
			}

			if !equalUnlock(fake.unlockCalls, tt.wantUnlock) {
				t.Errorf("Unlock calls: want %v, got %v", tt.wantUnlock, fake.unlockCalls)
			}
		})
	}
}

// TestUnaryRPCsSetDeadline verifies every unary RPC attaches a per-call deadline
// to the context it hands the client (see unaryTimeout). The streaming Run is
// intentionally excluded — it has no overall deadline.
func TestUnaryRPCsSetDeadline(t *testing.T) {
	fake := &fakeDeviceServiceClient{}
	app := newTestApp(t, fake)
	ctx := context.Background()

	calls := []struct {
		name string
		call func() error
	}{
		{"list", func() error { return app.listRPC(ctx) }},
		{"commands", func() error { return app.commandsRPC(ctx, "dev") }},
		{"details", func() error { return app.detailsRPC(ctx, "dev", "cmd", "help") }},
		{"lock", func() error { return app.lockRPC(ctx, "dev", nil) }},
		{"unlock", func() error { return app.unlockRPC(ctx, "dev", false) }},
	}

	for _, c := range calls {
		if err := c.call(); err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
	}

	if len(fake.sawDeadline) != len(calls) {
		t.Fatalf("recorded %d calls, want %d", len(fake.sawDeadline), len(calls))
	}

	for i, c := range calls {
		if !fake.sawDeadline[i] {
			t.Errorf("%s: RPC context has no deadline", c.name)
		}
	}
}

// TestUnaryRPCHonorsCancellation verifies every unary RPC runs under the caller's
// (signal) context: a cancelled parent aborts the call instead of proceeding on a
// fresh background context. Covering all five guards against an incomplete refactor
// where one method wraps context.Background() rather than the passed ctx — which
// would still show a deadline (so TestUnaryRPCsSetDeadline stays green) yet drop
// the caller's cancellation.
func TestUnaryRPCHonorsCancellation(t *testing.T) {
	calls := []struct {
		name string
		call func(app *application, ctx context.Context) error
	}{
		{"list", func(app *application, ctx context.Context) error { return app.listRPC(ctx) }},
		{"commands", func(app *application, ctx context.Context) error { return app.commandsRPC(ctx, "dev") }},
		{"details", func(app *application, ctx context.Context) error { return app.detailsRPC(ctx, "dev", "cmd", "help") }},
		{"lock", func(app *application, ctx context.Context) error { return app.lockRPC(ctx, "dev", nil) }},
		{"unlock", func(app *application, ctx context.Context) error { return app.unlockRPC(ctx, "dev", false) }},
	}

	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeDeviceServiceClient{respectCtx: true}
			app := newTestApp(t, fake)

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // already cancelled before the call

			err := c.call(app, ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s: want context.Canceled, got %v", c.name, err)
			}
		})
	}
}

// TestAsInterrupt covers the signal-to-errInterrupted mapping: an error is
// reported as an interrupt only when the shared signal context was cancelled (a
// fired signal), while a per-call timeout — which does not cancel that context —
// and a successful call are left untouched.
func TestAsInterrupt(t *testing.T) {
	signaled, cancel := context.WithCancel(context.Background())
	cancel() // mimics a fired SIGINT/SIGTERM

	live := context.Background() // no signal
	rpcErr := errors.New("boom")

	tests := []struct {
		name      string
		err       error
		signalCtx context.Context
		want      error
	}{
		{"signal fired with error maps to interrupted", rpcErr, signaled, errInterrupted},
		{"signal fired without error stays nil", nil, signaled, nil},
		{"no signal keeps the original error", rpcErr, live, rpcErr},
		{"no signal, no error", nil, live, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := asInterrupt(tt.signalCtx, tt.err); got != tt.want {
				t.Fatalf("asInterrupt = %v, want %v", got, tt.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func equalDetails(a, b []detailsCall) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func equalUnlock(a, b []unlockCall) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
