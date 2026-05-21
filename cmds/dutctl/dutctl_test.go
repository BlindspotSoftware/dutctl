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
}

type detailsCall struct {
	device, cmd, keyword string
}

func (f *fakeDeviceServiceClient) List(
	_ context.Context, _ *connect.Request[pb.ListRequest],
) (*connect.Response[pb.ListResponse], error) {
	f.listCalls++

	if f.listErr != nil {
		return nil, f.listErr
	}

	return connect.NewResponse(&pb.ListResponse{Devices: f.listDevices}), nil
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
		cmd:     req.Msg.GetCmd(),
		keyword: req.Msg.GetKeyword(),
	})

	return connect.NewResponse(&pb.DetailsResponse{}), nil
}

func (f *fakeDeviceServiceClient) Run(
	_ context.Context,
) *connect.BidiStreamForClient[pb.RunRequest, pb.RunResponse] {
	return nil
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
			listDevices: []string{"mydevice"},
			wantListHit: 1,
			wantCmdHits: []string{"mydevice"},
		},
		{
			name:        "device command help calls details",
			args:        []string{"mydevice", "power", "help"},
			listDevices: []string{"mydevice"},
			wantListHit: 1,
			wantDetailHi: []detailsCall{
				{device: "mydevice", cmd: "power", keyword: "help"},
			},
		},
		{
			name:        "single device auto-resolves help into details",
			args:        []string{"power", "help"},
			listDevices: []string{"onlydev"},
			wantListHit: 1,
			wantDetailHi: []detailsCall{
				{device: "onlydev", cmd: "power", keyword: "help"},
			},
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
		})
	}
}

func TestMaybeResolveSingleDevice(t *testing.T) {
	errBoom := errors.New("boom")

	tests := []struct {
		name        string
		args        []string
		listDevices []string
		listErr     error
		want        []string
	}{
		{
			name: "empty args returns empty",
			args: nil,
			want: nil,
		},
		{
			name:        "single device matching first arg is unchanged",
			args:        []string{"only"},
			listDevices: []string{"only"},
			want:        []string{"only"},
		},
		{
			name:        "single device differing from first arg is prepended",
			args:        []string{"power", "on"},
			listDevices: []string{"only"},
			want:        []string{"only", "power", "on"},
		},
		{
			name:        "multiple devices: no rewrite",
			args:        []string{"power", "on"},
			listDevices: []string{"a", "b"},
			want:        []string{"power", "on"},
		},
		{
			name:        "zero devices: no rewrite",
			args:        []string{"power"},
			listDevices: []string{},
			want:        []string{"power"},
		},
		{
			name:    "list RPC failure: no rewrite",
			args:    []string{"power", "on"},
			listErr: errBoom,
			want:    []string{"power", "on"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeDeviceServiceClient{listDevices: tt.listDevices, listErr: tt.listErr}
			app := newTestApp(t, fake)

			got := app.maybeResolveSingleDevice(tt.args)

			if !equalStrings(got, tt.want) {
				t.Errorf("resolve: want %v, got %v", tt.want, got)
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
