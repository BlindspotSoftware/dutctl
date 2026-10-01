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

	if le.Holder.Kind != Reserved || le.Holder.Owner != "alice" {
		t.Errorf("Error = %+v, want kind=reserved owner=alice", le)
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

	if le.Holder.Kind != Busy || le.Holder.Owner != "alice" {
		t.Errorf("Error = %+v, want kind=busy owner=alice", le)
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

	hold, ok := l.StatusAll()["dev"]
	if !ok || hold.Kind != Busy {
		t.Fatalf("StatusAll[dev] = %+v (ok=%v), want a Busy hold", hold, ok)
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

	if le.Holder.Kind != Reserved {
		t.Errorf("blocking kind = %q, want reserved", le.Holder.Kind)
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

	hold, ok := l.StatusAll()["dev"]
	if !ok || hold.Kind != Reserved {
		t.Fatalf("StatusAll[dev] = %+v (ok=%v), want the reservation intact", hold, ok)
	}

	// Releasing the reservation must leave the device free, proving the Busy
	// hold really was cleared rather than merely shadowed by the reservation.
	if err := l.ClearLock("dev", "alice"); err != nil {
		t.Fatalf("ClearLock: %v", err)
	}

	if _, ok := l.StatusAll()["dev"]; ok {
		t.Error("Busy hold still present after ClearAutoLock")
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

func TestForceClearLockReleasesReservation(t *testing.T) {
	l := New()

	if _, err := l.Lock("dev", "alice", time.Hour); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	if err := l.ForceClearLock("dev"); err != nil {
		t.Fatalf("ForceClearLock: %v", err)
	}

	if hold, ok := l.StatusAll()["dev"]; ok {
		t.Errorf("StatusAll[dev] = %+v after ForceClearLock, want free", hold)
	}

	if err := l.ForceClearLock("dev"); !errors.Is(err, ErrNotLocked) {
		t.Errorf("ForceClearLock on free device: err = %v, want ErrNotLocked", err)
	}
}

// A forced unlock breaks a reservation, never a running command: the Busy hold
// survives it, and the device stays busy for everyone else until the command's
// own release.
func TestForceClearLockLeavesBusyHold(t *testing.T) {
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

	hold, ok := l.StatusAll()["dev"]
	if !ok || hold.Kind != Busy || hold.Owner != "alice" {
		t.Fatalf("StatusAll[dev] = %+v (ok=%v) after ForceClearLock, want alice's Busy hold", hold, ok)
	}

	var le *Error
	if _, err := l.AutoLock("dev", "bob"); !errors.As(err, &le) || le.Holder.Kind != Busy {
		t.Errorf("bob's AutoLock after ForceClearLock: err = %v, want a *Error naming the Busy hold", err)
	}

	if err := l.ForceClearLock("dev"); !errors.Is(err, ErrBusy) {
		t.Errorf("second ForceClearLock: err = %v, want ErrBusy", err)
	}

	if err := l.ClearAutoLock("dev", "alice"); err != nil {
		t.Fatalf("ClearAutoLock: %v", err)
	}

	if hold, ok := l.StatusAll()["dev"]; ok {
		t.Errorf("StatusAll[dev] = %+v after the command's release, want free", hold)
	}
}

// A device that runs a command but has no reservation has nothing a forced
// unlock may release: it says so, naming the command's owner, and frees nothing.
func TestForceClearLockRejectsBusyDevice(t *testing.T) {
	l := New()

	if _, err := l.AutoLock("dev", "alice"); err != nil {
		t.Fatalf("AutoLock: %v", err)
	}

	err := l.ForceClearLock("dev")
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("ForceClearLock: err = %v, want ErrBusy", err)
	}

	if errors.Is(err, ErrNotLocked) || errors.Is(err, ErrWrongOwner) {
		t.Errorf("ForceClearLock: err = %v matches ErrNotLocked or ErrWrongOwner, want ErrBusy only", err)
	}

	if want := `device "dev" is running a command for "alice", which a forced unlock does not end`; err.Error() != want {
		t.Errorf("ForceClearLock: message = %q, want %q", err.Error(), want)
	}

	hold, ok := l.StatusAll()["dev"]
	if !ok || hold.Kind != Busy || hold.Owner != "alice" {
		t.Fatalf("StatusAll[dev] = %+v (ok=%v) after ForceClearLock, want alice's Busy hold", hold, ok)
	}

	if err := l.ClearAutoLock("dev", "alice"); err != nil {
		t.Errorf("ClearAutoLock after the rejected ForceClearLock: %v", err)
	}
}

// A reservation that has run out is no reservation: a forced unlock of a device
// whose reservation has expired but which still runs a command reports the
// command, not a released reservation.
func TestForceClearLockExpiredReservationWhileBusy(t *testing.T) {
	l := New()

	if _, err := l.Lock("dev", "alice", time.Millisecond); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	if _, err := l.AutoLock("dev", "alice"); err != nil {
		t.Fatalf("AutoLock: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	if err := l.ForceClearLock("dev"); !errors.Is(err, ErrBusy) {
		t.Errorf("ForceClearLock after the reservation expired: err = %v, want ErrBusy", err)
	}
}

func TestStatusAllReportsEffectiveHold(t *testing.T) {
	l := New()

	if _, err := l.Lock("alpha", "alice", time.Hour); err != nil {
		t.Fatalf("Lock alpha: %v", err)
	}

	if _, err := l.AutoLock("beta", "bob"); err != nil {
		t.Fatalf("AutoLock beta: %v", err)
	}

	// gamma is both reserved and busy by the same owner.
	if _, err := l.Lock("gamma", "carol", time.Hour); err != nil {
		t.Fatalf("Lock gamma: %v", err)
	}

	if _, err := l.AutoLock("gamma", "carol"); err != nil {
		t.Fatalf("AutoLock gamma: %v", err)
	}

	status := l.StatusAll()

	if got := status["alpha"]; got.Kind != Reserved || got.Owner != "alice" {
		t.Errorf("alpha = %+v, want a Reserved hold owned by alice", got)
	}

	if got := status["beta"]; got.Kind != Busy || got.Owner != "bob" {
		t.Errorf("beta = %+v, want a Busy hold owned by bob", got)
	}

	// A live reservation shadows the concurrent Busy hold in the report.
	if got := status["gamma"]; got.Kind != Reserved || got.Owner != "carol" {
		t.Errorf("gamma = %+v, want the reservation to shadow the Busy hold", got)
	}
}
