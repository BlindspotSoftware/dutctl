// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package locker

import (
	"errors"
	"testing"
	"time"
)

func TestLockHappyPath(t *testing.T) {
	l := New()

	info, err := l.Lock("dev", "alice", time.Minute)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}

	if info.Owner != "alice" {
		t.Errorf("Owner = %q, want alice", info.Owner)
	}

	if info.ExpiresAt.IsZero() {
		t.Error("ExpiresAt is zero, want a timed expiry")
	}

	if err := l.CheckAccess("dev", "alice"); err != nil {
		t.Errorf("CheckAccess for owner: %v", err)
	}

	if err := l.ClearLock("dev", "alice"); err != nil {
		t.Errorf("ClearLock: %v", err)
	}
}

func TestLockRejectsNonPositiveDuration(t *testing.T) {
	l := New()

	for _, dur := range []time.Duration{0, -time.Second, -time.Hour} {
		_, err := l.Lock("dev", "alice", dur)
		if !errors.Is(err, ErrInvalidDuration) {
			t.Errorf("Lock dur=%v: err = %v, want ErrInvalidDuration", dur, err)
		}
	}
}

func TestLockSameOwnerExtend(t *testing.T) {
	l := New()

	first, err := l.Lock("dev", "alice", time.Minute)
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}

	second, err := l.Lock("dev", "alice", time.Hour)
	if err != nil {
		t.Fatalf("extend Lock: %v", err)
	}

	if !second.ExpiresAt.After(first.ExpiresAt) {
		t.Errorf("extend did not push expiry out: first=%v second=%v", first.ExpiresAt, second.ExpiresAt)
	}

	third, err := l.Lock("dev", "alice", time.Minute)
	if err != nil {
		t.Fatalf("shorter re-lock: %v", err)
	}

	if third.ExpiresAt.Before(second.ExpiresAt) {
		t.Errorf("shorter re-lock shrank expiry: second=%v third=%v", second.ExpiresAt, third.ExpiresAt)
	}
}

func TestLockBlockedByDifferentOwnerExplicit(t *testing.T) {
	l := New()

	if _, err := l.Lock("dev", "alice", time.Minute); err != nil {
		t.Fatalf("setup Lock: %v", err)
	}

	_, err := l.Lock("dev", "bob", time.Minute)

	var le *Error
	if !errors.As(err, &le) {
		t.Fatalf("Lock by other owner: err = %v, want *Error", err)
	}

	if le.Holder.Slot != ExplicitSlot || le.Holder.Owner != "alice" {
		t.Errorf("Error = %+v, want slot=explicit owner=alice", le)
	}
}

func TestLockBlockedByDifferentOwnerAuto(t *testing.T) {
	l := New()

	if _, err := l.AutoLock("dev", "alice"); err != nil {
		t.Fatalf("setup AutoLock: %v", err)
	}

	_, err := l.Lock("dev", "bob", time.Minute)

	var le *Error
	if !errors.As(err, &le) {
		t.Fatalf("Lock blocked by auto: err = %v, want *Error", err)
	}

	if le.Holder.Slot != AutoSlot || le.Holder.Owner != "alice" {
		t.Errorf("Error = %+v, want slot=auto owner=alice", le)
	}
}

func TestLockExplicitExpires(t *testing.T) {
	l := New()

	if _, err := l.Lock("dev", "alice", time.Millisecond); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	if _, err := l.Lock("dev", "bob", time.Minute); err != nil {
		t.Errorf("Lock after expiry: %v", err)
	}
}

func TestStatusAllPrunesExpired(t *testing.T) {
	l := New()

	if _, err := l.Lock("dev", "alice", time.Millisecond); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	if _, ok := l.StatusAll()["dev"]; ok {
		t.Error("StatusAll still reports the device after its explicit lock expired")
	}
}

func TestClearLockErrors(t *testing.T) {
	l := New()

	if err := l.ClearLock("dev", "alice"); !errors.Is(err, ErrNotLocked) {
		t.Errorf("ClearLock on free slot: err = %v, want ErrNotLocked", err)
	}

	if _, err := l.Lock("dev", "alice", time.Minute); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	if err := l.ClearLock("dev", "bob"); !errors.Is(err, ErrWrongOwner) {
		t.Errorf("ClearLock by wrong owner: err = %v, want ErrWrongOwner", err)
	}
}

func TestAutoLockNoExpiry(t *testing.T) {
	l := New()

	info, err := l.AutoLock("dev", "alice")
	if err != nil {
		t.Fatalf("AutoLock: %v", err)
	}

	if !info.ExpiresAt.IsZero() {
		t.Errorf("auto-lock ExpiresAt = %v, want zero", info.ExpiresAt)
	}

	state, ok := l.StatusAll()["dev"]
	if !ok || state.Auto == nil {
		t.Fatal("auto-lock missing from StatusAll")
	}

	if state.Explicit != nil {
		t.Error("AutoLock unexpectedly populated the explicit slot")
	}
}

func TestAutoLockSameOwnerIdempotent(t *testing.T) {
	l := New()

	first, err := l.AutoLock("dev", "alice")
	if err != nil {
		t.Fatalf("first AutoLock: %v", err)
	}

	second, err := l.AutoLock("dev", "alice")
	if err != nil {
		t.Fatalf("second AutoLock: %v", err)
	}

	if !second.LockedAt.Equal(first.LockedAt) {
		t.Errorf("re-AutoLock changed LockedAt: first=%v second=%v", first.LockedAt, second.LockedAt)
	}
}

func TestAutoLockBlockedByExplicitOtherOwner(t *testing.T) {
	l := New()

	if _, err := l.Lock("dev", "alice", time.Minute); err != nil {
		t.Fatalf("setup Lock: %v", err)
	}

	_, err := l.AutoLock("dev", "bob")

	var le *Error
	if !errors.As(err, &le) {
		t.Fatalf("AutoLock blocked by explicit: err = %v, want *Error", err)
	}

	if le.Holder.Slot != ExplicitSlot {
		t.Errorf("blocking slot = %q, want explicit", le.Holder.Slot)
	}
}

func TestClearAutoLockLeavesExplicitIntact(t *testing.T) {
	l := New()

	if _, err := l.Lock("dev", "alice", time.Hour); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	if _, err := l.AutoLock("dev", "alice"); err != nil {
		t.Fatalf("AutoLock: %v", err)
	}

	if err := l.ClearAutoLock("dev", "alice"); err != nil {
		t.Fatalf("ClearAutoLock: %v", err)
	}

	state, ok := l.StatusAll()["dev"]
	if !ok || state.Explicit == nil {
		t.Fatal("explicit lock was wiped by ClearAutoLock")
	}

	if state.Auto != nil {
		t.Error("auto lock still present after ClearAutoLock")
	}
}

func TestClearAutoLockErrors(t *testing.T) {
	l := New()

	if err := l.ClearAutoLock("dev", "alice"); !errors.Is(err, ErrNotLocked) {
		t.Errorf("ClearAutoLock on free slot: err = %v, want ErrNotLocked", err)
	}

	if _, err := l.AutoLock("dev", "alice"); err != nil {
		t.Fatalf("AutoLock: %v", err)
	}

	if err := l.ClearAutoLock("dev", "bob"); !errors.Is(err, ErrWrongOwner) {
		t.Errorf("ClearAutoLock by wrong owner: err = %v, want ErrWrongOwner", err)
	}
}

func TestForceClearLockWipesBothSlots(t *testing.T) {
	l := New()

	if _, err := l.Lock("dev", "alice", time.Hour); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	if _, err := l.AutoLock("dev", "alice"); err != nil {
		t.Fatalf("AutoLock: %v", err)
	}

	if err := l.ForceClearLock("dev"); err != nil {
		t.Fatalf("ForceClearLock: %v", err)
	}

	if _, ok := l.StatusAll()["dev"]; ok {
		t.Error("device still appears in StatusAll after ForceClearLock")
	}

	if err := l.ForceClearLock("dev"); !errors.Is(err, ErrNotLocked) {
		t.Errorf("ForceClearLock on free device: err = %v, want ErrNotLocked", err)
	}
}

func TestStatusAllReportsBothSlotsIndependently(t *testing.T) {
	l := New()

	if _, err := l.Lock("alpha", "alice", time.Hour); err != nil {
		t.Fatalf("Lock alpha: %v", err)
	}

	if _, err := l.AutoLock("beta", "bob"); err != nil {
		t.Fatalf("AutoLock beta: %v", err)
	}

	if _, err := l.Lock("gamma", "carol", time.Hour); err != nil {
		t.Fatalf("Lock gamma: %v", err)
	}

	if _, err := l.AutoLock("gamma", "carol"); err != nil {
		t.Fatalf("AutoLock gamma: %v", err)
	}

	status := l.StatusAll()

	if got := status["alpha"]; got.Explicit == nil || got.Auto != nil {
		t.Errorf("alpha = %+v, want explicit-only", got)
	}

	if got := status["beta"]; got.Auto == nil || got.Explicit != nil {
		t.Errorf("beta = %+v, want auto-only", got)
	}

	if got := status["gamma"]; got.Explicit == nil || got.Auto == nil {
		t.Errorf("gamma = %+v, want both slots populated", got)
	}
}

func TestCheckAccessAllowsSameOwnerOnBothSlots(t *testing.T) {
	l := New()

	if _, err := l.Lock("dev", "alice", time.Hour); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	if _, err := l.AutoLock("dev", "alice"); err != nil {
		t.Fatalf("AutoLock: %v", err)
	}

	if err := l.CheckAccess("dev", "alice"); err != nil {
		t.Errorf("CheckAccess for same owner: %v", err)
	}

	err := l.CheckAccess("dev", "bob")

	var le *Error
	if !errors.As(err, &le) {
		t.Fatalf("CheckAccess for other owner: err = %v, want *Error", err)
	}
}
