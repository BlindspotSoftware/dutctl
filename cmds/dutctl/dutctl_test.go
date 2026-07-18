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
}

type detailsCall struct {
	device, cmd, keyword string
}

type unlockCall struct {
	device string
	force  bool
}

func (f *fakeDeviceServiceClient) List(
	_ context.Context, _ *connect.Request[pb.ListRequest],
) (*connect.Response[pb.ListResponse], error) {
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
	_ context.Context, req *connect.Request[pb.CommandsRequest],
) (*connect.Response[pb.CommandsResponse], error) {
	f.commandsCalls = append(f.commandsCalls, req.Msg.GetDevice())

	return connect.NewResponse(&pb.CommandsResponse{}), nil
}

func (f *fakeDeviceServiceClient) Details(
	_ context.Context, req *connect.Request[pb.DetailsRequest],
) (*connect.Response[pb.DetailsResponse], error) {
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
	_ context.Context, _ *connect.Request[pb.LockRequest],
) (*connect.Response[pb.LockResponse], error) {
	return connect.NewResponse(&pb.LockResponse{}), nil
}

func (f *fakeDeviceServiceClient) Unlock(
	_ context.Context, req *connect.Request[pb.UnlockRequest],
) (*connect.Response[pb.UnlockResponse], error) {
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
