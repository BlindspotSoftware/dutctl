// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package main

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/auth"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/locker"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

func newTestService() *rpcService {
	return &rpcService{
		devices: dut.Devlist{"devA": dut.Device{}, "otherDev": dut.Device{}},
		locker:  locker.New(),
	}
}

// userCtx seeds a context with the caller identity the way the header
// interceptor does on the real request path, so the handlers under test read it
// back via auth.FromContext.
func userCtx(user string) context.Context {
	return auth.NewContext(context.Background(), auth.Named(user))
}

func anonCtx() context.Context {
	return auth.NewContext(context.Background(), auth.Anonymous())
}

func lockReq(device string, durSeconds int64) *connect.Request[pb.LockRequest] {
	return connect.NewRequest(&pb.LockRequest{Device: device, DurationSeconds: durSeconds})
}

func unlockReq(device string, force bool) *connect.Request[pb.UnlockRequest] {
	return connect.NewRequest(&pb.UnlockRequest{Device: device, Force: force})
}

func TestLockRPC(t *testing.T) {
	svc := newTestService()

	res, err := svc.Lock(userCtx("alice"), lockReq("devA", 60))
	if err != nil {
		t.Fatalf("Lock: unexpected error: %v", err)
	}

	if res.Msg.GetLock().GetOwner() != "alice" {
		t.Errorf("owner = %q, want alice", res.Msg.GetLock().GetOwner())
	}

	if res.Msg.GetLock().GetExpiresAt() == 0 {
		t.Error("expires_at = 0, want a timed expiry")
	}
}

func TestLockRPCUnknownDevice(t *testing.T) {
	svc := newTestService()

	_, err := svc.Lock(userCtx("alice"), lockReq("ghost", 60))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

func TestLockRPCDifferentOwnerRejected(t *testing.T) {
	svc := newTestService()

	if _, err := svc.Lock(userCtx("alice"), lockReq("devA", 60)); err != nil {
		t.Fatalf("first Lock: %v", err)
	}

	_, err := svc.Lock(userCtx("bob"), lockReq("devA", 60))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

func TestLockRPCRejectsAnonymous(t *testing.T) {
	svc := newTestService()

	// An anonymous caller gets a fresh identity per request, so it could never
	// release a lock it took; Lock rejects it rather than create a stuck lock.
	_, err := svc.Lock(anonCtx(), lockReq("devA", 60))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", connect.CodeOf(err))
	}
}

func TestUnlockRPCAnonymous(t *testing.T) {
	t.Run("non_force_rejected", func(t *testing.T) {
		svc := newTestService()

		_, err := svc.Unlock(anonCtx(), unlockReq("devA", false))
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Errorf("code = %v, want Unauthenticated", connect.CodeOf(err))
		}
	})

	t.Run("force_allowed", func(t *testing.T) {
		svc := newTestService()

		// alice holds the lock; an anonymous caller may still force-release it.
		if _, err := svc.Lock(userCtx("alice"), lockReq("devA", 60)); err != nil {
			t.Fatalf("setup Lock: %v", err)
		}

		if _, err := svc.Unlock(anonCtx(), unlockReq("devA", true)); err != nil {
			t.Errorf("anonymous force-unlock: unexpected error: %v", err)
		}
	})
}

func TestUnlockRPC(t *testing.T) {
	svc := newTestService()

	if _, err := svc.Lock(userCtx("alice"), lockReq("devA", 60)); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	if _, err := svc.Unlock(userCtx("alice"), unlockReq("devA", false)); err != nil {
		t.Errorf("Unlock by owner: %v", err)
	}
}

func TestUnlockRPCWrongOwner(t *testing.T) {
	svc := newTestService()

	if _, err := svc.Lock(userCtx("alice"), lockReq("devA", 60)); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	_, err := svc.Unlock(userCtx("bob"), unlockReq("devA", false))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", connect.CodeOf(err))
	}
}

func TestUnlockRPCNotLocked(t *testing.T) {
	svc := newTestService()

	_, err := svc.Unlock(userCtx("alice"), unlockReq("devA", false))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

func TestUnlockRPCForce(t *testing.T) {
	svc := newTestService()

	if _, err := svc.Lock(userCtx("alice"), lockReq("devA", 60)); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	if _, err := svc.Unlock(userCtx("bob"), unlockReq("devA", true)); err != nil {
		t.Errorf("forced Unlock by non-owner: %v", err)
	}
}

func TestLockRPCDurationBoundaries(t *testing.T) {
	t.Run("zero_applies_agent_default", func(t *testing.T) {
		svc := newTestService()

		before := time.Now()

		res, err := svc.Lock(userCtx("alice"), lockReq("devA", 0))
		if err != nil {
			t.Fatalf("Lock with zero duration: unexpected error: %v", err)
		}

		got := time.Unix(res.Msg.GetLock().GetExpiresAt(), 0).Sub(before)
		if got < defaultLockDuration-time.Minute || got > defaultLockDuration+time.Minute {
			t.Errorf("expiry in %v, want about the agent default %v", got, defaultLockDuration)
		}
	})

	t.Run("negative_rejected", func(t *testing.T) {
		svc := newTestService()

		_, err := svc.Lock(userCtx("alice"), lockReq("devA", -5))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("code = %v, want InvalidArgument", connect.CodeOf(err))
		}
	})
}

func TestListRPCShowsAutoOnlyLock(t *testing.T) {
	svc := newTestService()

	if _, err := svc.locker.AutoLock("devA", "alice"); err != nil {
		t.Fatalf("AutoLock: %v", err)
	}

	res, err := svc.List(context.Background(), connect.NewRequest(&pb.ListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var got *pb.LockState

	for _, info := range res.Msg.GetDevices() {
		if info.GetName() == "devA" {
			got = info.GetLock()
		}
	}

	if got == nil {
		t.Fatal("auto-only lock hidden in List, want it surfaced as busy")
	}

	if got.GetOwner() != "alice" {
		t.Errorf("owner = %q, want alice", got.GetOwner())
	}

	// An auto-lock has no time-based expiry; it surfaces as 0 ("in use").
	if got.GetExpiresAt() != 0 {
		t.Errorf("expires_at = %d, want 0 for an auto-lock", got.GetExpiresAt())
	}
}

func TestListRPCExplicitShadowsAuto(t *testing.T) {
	svc := newTestService()

	if _, err := svc.locker.AutoLock("devA", "alice"); err != nil {
		t.Fatalf("AutoLock: %v", err)
	}

	if _, err := svc.locker.Lock("devA", "alice", time.Minute); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	res, err := svc.List(context.Background(), connect.NewRequest(&pb.ListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var got *pb.LockState

	for _, info := range res.Msg.GetDevices() {
		if info.GetName() == "devA" {
			got = info.GetLock()
		}
	}

	if got.GetExpiresAt() == 0 {
		t.Error("expected explicit-slot expires_at to win, got 0")
	}
}
