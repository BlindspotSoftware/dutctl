// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/session"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/pkg/module"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// slowReleaseModule holds a device, like the serial module holds its port, and
// needs releaseAfter to let go of it once the run is canceled. If ignoreCancel
// is set it keeps running until unblock is closed instead.
type slowReleaseModule struct {
	started      chan struct{}
	releaseAfter time.Duration
	ignoreCancel bool
	unblock      chan struct{}
	released     atomic.Bool
}

func (m *slowReleaseModule) Help() string                   { return "slow release" }
func (m *slowReleaseModule) Init(_ context.Context) error   { return nil }
func (m *slowReleaseModule) Deinit(_ context.Context) error { return nil }
func (m *slowReleaseModule) Run(ctx context.Context, _ module.Session, _ ...string) error {
	close(m.started)

	if m.ignoreCancel {
		<-m.unblock
	} else {
		<-ctx.Done()
		time.Sleep(m.releaseAfter)
	}

	m.released.Store(true)

	return nil
}

// startCanceledRun starts mod through executeModules, waits until it runs,
// then cancels the run as a client quitting would. A non-zero grace replaces
// the default wait for the module to stop.
func startCanceledRun(t *testing.T, mod *slowReleaseModule, grace time.Duration) (context.Context, runCmdArgs, *session.Broker) {
	t.Helper()

	stream := &blockingStream{closed: make(chan struct{})}
	t.Cleanup(func() { close(stream.closed) })

	dmod := dut.Module{Module: mod}
	dmod.Config.Name = "slowRelease"
	dmod.Config.Passthrough = true

	ctx, cancel := context.WithCancel(context.Background())
	broker := &session.Broker{}

	args, _, err := executeModules(ctx, runCmdArgs{
		stream:    stream,
		broker:    broker,
		cmdMsg:    &pb.Command{Device: "devX", Command: "serial"},
		cmd:       dut.Command{Modules: []dut.Module{dmod}},
		stopGrace: grace,
	})
	if err != nil {
		t.Fatalf("executeModules: %v", err)
	}

	<-mod.started
	cancel()

	return ctx, args, broker
}

// TestWaitModulesWaitsForModuleToReleaseDevice checks that a canceled run does
// not end, and so does not hand the device on, before the module let go of it.
func TestWaitModulesWaitsForModuleToReleaseDevice(t *testing.T) {
	mod := &slowReleaseModule{started: make(chan struct{}), releaseAfter: 150 * time.Millisecond}

	ctx, args, broker := startCanceledRun(t, mod, 0)

	_, _, err := waitModules(ctx, args)
	if connect.CodeOf(err) != connect.CodeCanceled {
		t.Errorf("waitModules error = %v, want CodeCanceled", err)
	}

	if !mod.released.Load() {
		t.Error("waitModules returned while the module still held the device")
	}

	broker.Wait()
}

// TestWaitModulesStopsWaitingAfterGrace checks that a module ignoring the
// cancellation cannot hold the run open for longer than the grace period.
func TestWaitModulesStopsWaitingAfterGrace(t *testing.T) {
	const grace = 50 * time.Millisecond

	mod := &slowReleaseModule{started: make(chan struct{}), ignoreCancel: true, unblock: make(chan struct{})}
	defer close(mod.unblock)

	ctx, args, broker := startCanceledRun(t, mod, grace)

	start := time.Now()

	_, _, err := waitModules(ctx, args)
	if connect.CodeOf(err) != connect.CodeCanceled {
		t.Errorf("waitModules error = %v, want CodeCanceled", err)
	}

	if took := time.Since(start); took > time.Second {
		t.Errorf("waitModules took %v, want about the grace period (%v)", took, grace)
	}

	broker.Wait()
}
