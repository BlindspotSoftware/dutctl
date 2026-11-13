// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/go-cmp/cmp"

	"github.com/BlindspotSoftware/dutctl/internal/output"
	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// newFetchFake returns a fake agent with a free and a reserved device, two
// commands, a help text and a lock to grant.
func newFetchFake(expires int64) *fakeDeviceServiceClient {
	return &fakeDeviceServiceClient{
		listDevices: []string{"free", "held"},
		listLocks:   map[string]*pb.LockState{"held": {Owner: "bob@desk", ExpiresAt: expires}},
		commands:    []string{"power", "console"},
		details:     "power on|off",
		lock:        &pb.LockState{Owner: "ada@lab", ExpiresAt: expires},
	}
}

// The fetchers return the agent's answer as data, lock state included, and
// send the user with the calls that act for the user.
func TestAgentClientFetchers(t *testing.T) {
	expires := time.Now().Add(time.Hour).Unix()
	fake := newFetchFake(expires)
	ac := agentClient{addr: "lab:2024", user: "ada@lab", client: fake}
	ctx := context.Background()

	devices, err := ac.listDevices(ctx)
	if err != nil {
		t.Fatalf("listDevices: %v", err)
	}

	wantDevices := []output.DeviceEntry{{Name: "free"}, {Name: "held", Locked: true, Owner: "bob@desk", ExpiresAt: expires}}
	if !slices.Equal(devices, wantDevices) {
		t.Errorf("listDevices = %v, want %v", devices, wantDevices)
	}

	commands, err := ac.deviceCommands(ctx, "held")
	if err != nil || !slices.Equal(commands, fake.commands) {
		t.Errorf("deviceCommands = %v, %v, want %v", commands, err, fake.commands)
	}

	details, err := ac.commandDetails(ctx, "held", "power", "help")
	if err != nil || details != fake.details {
		t.Errorf("commandDetails = %q, %v, want %q", details, err, fake.details)
	}

	entry, err := ac.lockDevice(ctx, "held", 2*time.Hour)
	if err != nil {
		t.Fatalf("lockDevice: %v", err)
	}

	if want := (output.DeviceEntry{Name: "held", Locked: true, Owner: "ada@lab", ExpiresAt: expires}); entry != want {
		t.Errorf("lockDevice = %v, want %v", entry, want)
	}

	if got := fake.lockReqs[0].GetDurationSeconds(); got != 7200 {
		t.Errorf("lock requested %d seconds, want 7200", got)
	}

	if err := ac.unlockDevice(ctx, "held", true); err != nil {
		t.Fatalf("unlockDevice: %v", err)
	}

	if want := []unlockCall{{device: "held", force: true}}; !equalUnlock(fake.unlockCalls, want) {
		t.Errorf("Unlock calls = %v, want %v", fake.unlockCalls, want)
	}

	if want := []string{"ada@lab", "ada@lab"}; !slices.Equal(fake.users, want) {
		t.Errorf("user headers of Lock and Unlock = %q, want %q", fake.users, want)
	}
}

// A fetcher hands the agent's error back unchanged, so the caller can tell
// its code.
func TestAgentClientFetchersReturnTheAgentsError(t *testing.T) {
	agentErr := connect.NewError(connect.CodeNotFound, errors.New("no such device"))
	ac := agentClient{client: &fakeDeviceServiceClient{
		listErr: agentErr, commandsErr: agentErr, detailsErr: agentErr, lockErr: agentErr, unlockErr: agentErr,
	}}
	ctx := context.Background()

	calls := []struct {
		name string
		call func() error
	}{
		{"list", func() error { _, err := ac.listDevices(ctx); return err }},
		{"commands", func() error { _, err := ac.deviceCommands(ctx, "dev"); return err }},
		{"details", func() error { _, err := ac.commandDetails(ctx, "dev", "cmd", "help"); return err }},
		{"lock", func() error { _, err := ac.lockDevice(ctx, "dev", 0); return err }},
		{"unlock", func() error { return ac.unlockDevice(ctx, "dev", false) }},
	}

	for _, c := range calls {
		if err := c.call(); !errors.Is(err, agentErr) {
			t.Errorf("%s: error = %v, want the agent's", c.name, err)
		}
	}
}

// recordingFormatter keeps the content the client renders; only WriteContent
// may be called.
type recordingFormatter struct {
	output.Formatter

	contents []output.Content
}

func (f *recordingFormatter) WriteContent(content output.Content) {
	f.contents = append(f.contents, content)
}

// The command line's unary calls render what the fetchers return as they did
// before the fetchers existed: same content type, data and metadata.
func TestUnaryRPCsRenderTheFetchedData(t *testing.T) {
	expires := time.Now().Add(time.Hour).Unix()
	rec := &recordingFormatter{}
	app := newTestApp(t, newFetchFake(expires))
	app.serverAddr = "lab:2024"
	app.user = "ada@lab"
	app.formatter = rec
	ctx := context.Background()

	for _, call := range []func() error{
		func() error { return app.listRPC(ctx) },
		func() error { return app.commandsRPC(ctx, "held") },
		func() error { return app.detailsRPC(ctx, "held", "power", "help") },
		func() error { return app.lockRPC(ctx, "held", []string{"2h"}) },
		func() error { return app.unlockRPC(ctx, "held", false) },
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}

	server := output.MetaServer
	want := []output.Content{
		{
			Type:     output.TypeDeviceList,
			Data:     []output.DeviceEntry{{Name: "free"}, {Name: "held", Locked: true, Owner: "bob@desk", ExpiresAt: expires}},
			Metadata: map[string]string{server: "lab:2024", output.MetaMsg: "List Response"},
		},
		{
			Type:     output.TypeCommandList,
			Data:     []string{"power", "console"},
			Metadata: map[string]string{server: "lab:2024", output.MetaMsg: "Commands Response", output.MetaDevice: "held"},
		},
		{
			Type: output.TypeCommandDetail,
			Data: "power on|off",
			Metadata: map[string]string{
				server: "lab:2024", output.MetaMsg: "Details Response", output.MetaDevice: "held",
				output.MetaCommand: "power", "keyword": "help",
			},
		},
		{
			Type:     output.TypeLockResult,
			Data:     output.DeviceEntry{Name: "held", Locked: true, Owner: "ada@lab", ExpiresAt: expires},
			Metadata: map[string]string{server: "lab:2024", output.MetaMsg: "Lock Response"},
		},
		{
			Type:     output.TypeLockResult,
			Data:     output.DeviceEntry{Name: "held"},
			Metadata: map[string]string{server: "lab:2024", output.MetaMsg: "Unlock Response"},
		},
	}

	if diff := cmp.Diff(want, rec.contents); diff != "" {
		t.Errorf("rendered content (-want +got):\n%s", diff)
	}
}

func TestParseLockDuration(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    time.Duration
		wantErr bool
	}{
		{
			name: "no args defers to agent default",
			args: nil,
			want: 0,
		},
		{
			name: "empty arg defers to agent default",
			args: []string{""},
			want: 0,
		},
		{
			name: "explicit minutes",
			args: []string{"5m"},
			want: 5 * time.Minute,
		},
		{
			name: "explicit compound duration",
			args: []string{"1h30m"},
			want: 90 * time.Minute,
		},
		{
			name:    "unparseable duration",
			args:    []string{"banana"},
			wantErr: true,
		},
		{
			name:    "zero duration rejected",
			args:    []string{"0s"},
			wantErr: true,
		},
		{
			name:    "negative duration rejected",
			args:    []string{"-5m"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLockDuration(tt.args)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got duration %v", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.want {
				t.Errorf("duration = %v, want %v", got, tt.want)
			}
		})
	}
}
