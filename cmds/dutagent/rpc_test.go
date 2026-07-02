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
	"github.com/BlindspotSoftware/dutctl/internal/dutagent"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/pkg/lock"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

func newTestService() *rpcService {
	return &rpcService{
		devices: dut.Devlist{"devA": dut.Device{}, "otherDev": dut.Device{}},
		locker:  dutagent.NewLocker(),
	}
}

func lockReq(device, user string, durSeconds int64) *connect.Request[pb.LockRequest] {
	req := connect.NewRequest(&pb.LockRequest{Device: device, DurationSeconds: durSeconds})
	if user != "" {
		req.Header().Set(lock.UserHeader, user)
	}

	return req
}

func unlockReq(device, user string, force bool) *connect.Request[pb.UnlockRequest] {
	req := connect.NewRequest(&pb.UnlockRequest{Device: device, Force: force})
	if user != "" {
		req.Header().Set(lock.UserHeader, user)
	}

	return req
}

func TestLockRPC(t *testing.T) {
	svc := newTestService()

	res, err := svc.Lock(context.Background(), lockReq("devA", "alice", 60))
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

	_, err := svc.Lock(context.Background(), lockReq("ghost", "alice", 60))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

func TestLockRPCDifferentOwnerRejected(t *testing.T) {
	svc := newTestService()

	if _, err := svc.Lock(context.Background(), lockReq("devA", "alice", 60)); err != nil {
		t.Fatalf("first Lock: %v", err)
	}

	_, err := svc.Lock(context.Background(), lockReq("devA", "bob", 60))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

func TestLockRPCMissingUserHeader(t *testing.T) {
	svc := newTestService()

	first, err := svc.Lock(context.Background(), lockReq("devA", "", 60))
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}

	if !strings.HasPrefix(first.Msg.GetOwner(), "unknown-") {
		t.Errorf("owner = %q, want unknown-<rand> prefix", first.Msg.GetOwner())
	}

	// A second anonymous caller must get a distinct identity so they cannot
	// satisfy CheckAccess against the first caller's lock.
	second, err := svc.Lock(context.Background(), lockReq("otherDev", "", 60))
	if err != nil {
		t.Fatalf("second Lock: %v", err)
	}

	if first.Msg.GetOwner() == second.Msg.GetOwner() {
		t.Errorf("two anonymous callers shared identity %q", first.Msg.GetOwner())
	}
}

func TestUnlockRPC(t *testing.T) {
	svc := newTestService()

	if _, err := svc.Lock(context.Background(), lockReq("devA", "alice", 60)); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	if _, err := svc.Unlock(context.Background(), unlockReq("devA", "alice", false)); err != nil {
		t.Errorf("Unlock by owner: %v", err)
	}
}

func TestUnlockRPCWrongOwner(t *testing.T) {
	svc := newTestService()

	if _, err := svc.Lock(context.Background(), lockReq("devA", "alice", 60)); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	_, err := svc.Unlock(context.Background(), unlockReq("devA", "bob", false))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", connect.CodeOf(err))
	}
}

func TestUnlockRPCNotLocked(t *testing.T) {
	svc := newTestService()

	_, err := svc.Unlock(context.Background(), unlockReq("devA", "alice", false))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

func TestUnlockRPCForce(t *testing.T) {
	svc := newTestService()

	if _, err := svc.Lock(context.Background(), lockReq("devA", "alice", 60)); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	if _, err := svc.Unlock(context.Background(), unlockReq("devA", "bob", true)); err != nil {
		t.Errorf("forced Unlock by non-owner: %v", err)
	}
}

func TestLockRPCZeroDurationRejected(t *testing.T) {
	svc := newTestService()

	for _, dur := range []int64{0, -5} {
		_, err := svc.Lock(context.Background(), lockReq("devA", "alice", dur))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("dur=%d: code = %v, want InvalidArgument", dur, connect.CodeOf(err))
		}
	}
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

func TestVersionRPC(t *testing.T) {
	svc := newTestService()

	res, err := svc.Version(context.Background(), connect.NewRequest(&pb.VersionRequest{}))
	if err != nil {
		t.Fatalf("Version: %v", err)
	}

	// The handler returns buildinfo.VersionString(), which always yields a
	// non-empty, "Version:"-prefixed block even when built without VCS info.
	if v := res.Msg.GetVersion(); !strings.Contains(v, "Version") {
		t.Errorf("Version RPC returned %q, want a version string", v)
	}
}
