// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package main

import (
	"context"
	"strings"
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

	if res.Msg.GetOwner() != "alice" {
		t.Errorf("owner = %q, want alice", res.Msg.GetOwner())
	}

	if res.Msg.GetExpiresAt() == 0 {
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

func TestLockRPCAnonymousOwnersDoNotCollide(t *testing.T) {
	svc := newTestService()

	// The interceptor mints a fresh anonymous identity per header-less caller;
	// seed two of them and confirm the handler keeps them distinct so one cannot
	// satisfy CheckAccess against the other's lock.
	first, err := svc.Lock(anonCtx(), lockReq("devA", 60))
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}

	if !strings.HasPrefix(first.Msg.GetOwner(), "unknown-") {
		t.Errorf("owner = %q, want unknown-<rand> prefix", first.Msg.GetOwner())
	}

	second, err := svc.Lock(anonCtx(), lockReq("otherDev", 60))
	if err != nil {
		t.Fatalf("second Lock: %v", err)
	}

	if first.Msg.GetOwner() == second.Msg.GetOwner() {
		t.Errorf("two anonymous callers shared identity %q", first.Msg.GetOwner())
	}
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

		got := time.Unix(res.Msg.GetExpiresAt(), 0).Sub(before)
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

func TestListRPCHidesAutoOnlyLock(t *testing.T) {
	svc := newTestService()

	if _, err := svc.locker.AutoLock("devA", "alice"); err != nil {
		t.Fatalf("AutoLock: %v", err)
	}

	res, err := svc.List(context.Background(), connect.NewRequest(&pb.ListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var got *pb.LockInfo

	for _, info := range res.Msg.GetDevices() {
		if info.GetName() == "devA" {
			got = info.GetLock()
		}
	}

	if got != nil {
		t.Errorf("auto-only lock surfaced in List: %+v, want no lock info", got)
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

	var got *pb.LockInfo

	for _, info := range res.Msg.GetDevices() {
		if info.GetName() == "devA" {
			got = info.GetLock()
		}
	}

	if got.GetExpiresAt() == 0 {
		t.Error("expected explicit-slot expires_at to win, got 0")
	}
}
