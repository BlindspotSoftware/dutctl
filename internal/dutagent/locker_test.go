// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dutagent_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/dutagent"
)

const (
	testDevice = "board1"
	ownerA     = "alice@ws"
	ownerB     = "bob@ws"
)

func TestLock_HappyPath(t *testing.T) {
	l := dutagent.NewLocker()

	info, err := l.Lock(testDevice, ownerA, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.Owner != ownerA {
		t.Errorf("owner = %q, want %q", info.Owner, ownerA)
	}

	if info.ExpiresAt.IsZero() {
		t.Error("ExpiresAt is zero")
	}
}

func TestLock_Conflict(t *testing.T) {
	l := dutagent.NewLocker()

	if _, err := l.Lock(testDevice, ownerA, time.Minute); err != nil {
		t.Fatalf("first lock: %v", err)
	}

	_, err := l.Lock(testDevice, ownerB, time.Minute)
	if err == nil {
		t.Fatal("expected error for conflicting lock, got nil")
	}

	var lockErr *dutagent.LockedError
	if !errors.As(err, &lockErr) {
		t.Fatalf("expected *LockedError, got %T: %v", err, err)
	}

	if lockErr.Info.Owner != ownerA {
		t.Errorf("LockedError.Owner = %q, want %q", lockErr.Info.Owner, ownerA)
	}

	if !errors.Is(err, dutagent.ErrLocked) {
		t.Error("error should wrap ErrLocked")
	}
}

func TestLock_Extend_SameOwner(t *testing.T) {
	l := dutagent.NewLocker()

	first, err := l.Lock(testDevice, ownerA, time.Minute)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	second, err := l.Lock(testDevice, ownerA, 10*time.Minute)
	if err != nil {
		t.Fatalf("extend lock: %v", err)
	}

	if !second.ExpiresAt.After(first.ExpiresAt) {
		t.Error("extended lock should have a later ExpiresAt")
	}

	if second.LockedAt != first.LockedAt {
		t.Error("LockedAt should not change on extend")
	}
}

func TestLock_AfterExpiry(t *testing.T) {
	l := dutagent.NewLocker()

	if _, err := l.Lock(testDevice, ownerA, time.Nanosecond); err != nil {
		t.Fatalf("first lock: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	// Different owner can now lock because the first lock expired.
	info, err := l.Lock(testDevice, ownerB, time.Minute)
	if err != nil {
		t.Fatalf("lock after expiry: %v", err)
	}

	if info.Owner != ownerB {
		t.Errorf("owner = %q, want %q", info.Owner, ownerB)
	}
}

func TestUnlock_HappyPath(t *testing.T) {
	l := dutagent.NewLocker()

	if _, err := l.Lock(testDevice, ownerA, time.Minute); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := l.Unlock(testDevice, ownerA); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	_, locked := l.Status(testDevice)
	if locked {
		t.Error("device should be unlocked after Unlock")
	}
}

func TestUnlock_WrongOwner(t *testing.T) {
	l := dutagent.NewLocker()

	if _, err := l.Lock(testDevice, ownerA, time.Minute); err != nil {
		t.Fatalf("lock: %v", err)
	}

	err := l.Unlock(testDevice, ownerB)
	if err == nil {
		t.Fatal("expected error for wrong owner unlock")
	}

	if !errors.Is(err, dutagent.ErrWrongOwner) {
		t.Errorf("expected ErrWrongOwner, got %v", err)
	}
}

func TestUnlock_NotLocked(t *testing.T) {
	l := dutagent.NewLocker()

	err := l.Unlock(testDevice, ownerA)
	if !errors.Is(err, dutagent.ErrNotLocked) {
		t.Errorf("expected ErrNotLocked, got %v", err)
	}
}

func TestUnlock_AfterExpiry(t *testing.T) {
	l := dutagent.NewLocker()

	if _, err := l.Lock(testDevice, ownerA, time.Nanosecond); err != nil {
		t.Fatalf("lock: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	err := l.Unlock(testDevice, ownerA)
	if !errors.Is(err, dutagent.ErrNotLocked) {
		t.Errorf("expected ErrNotLocked after expiry, got %v", err)
	}
}

func TestStatus(t *testing.T) {
	l := dutagent.NewLocker()

	info, locked := l.Status(testDevice)
	if locked || info != nil {
		t.Error("should be unlocked initially")
	}

	if _, err := l.Lock(testDevice, ownerA, time.Minute); err != nil {
		t.Fatalf("lock: %v", err)
	}

	info, locked = l.Status(testDevice)
	if !locked {
		t.Error("should be locked after Lock")
	}

	if info.Owner != ownerA {
		t.Errorf("owner = %q, want %q", info.Owner, ownerA)
	}
}

func TestStatusAll(t *testing.T) {
	l := dutagent.NewLocker()

	devices := []string{"board1", "board2", "board3"}
	for _, d := range devices {
		if _, err := l.Lock(d, ownerA, time.Minute); err != nil {
			t.Fatalf("lock %q: %v", d, err)
		}
	}

	all := l.StatusAll()
	if len(all) != len(devices) {
		t.Errorf("StatusAll returned %d entries, want %d", len(all), len(devices))
	}

	for _, d := range devices {
		if _, ok := all[d]; !ok {
			t.Errorf("device %q missing from StatusAll", d)
		}
	}
}

func TestStatusAll_PrunesExpired(t *testing.T) {
	l := dutagent.NewLocker()

	if _, err := l.Lock("expired", ownerA, time.Nanosecond); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if _, err := l.Lock("active", ownerA, time.Minute); err != nil {
		t.Fatalf("lock: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	all := l.StatusAll()
	if len(all) != 1 {
		t.Errorf("expected 1 active lock, got %d", len(all))
	}

	if _, ok := all["active"]; !ok {
		t.Error("active device should be in StatusAll")
	}
}

func TestCheckAccess(t *testing.T) {
	l := dutagent.NewLocker()

	// Unlocked device: any owner has access.
	if err := l.CheckAccess(testDevice, ownerA); err != nil {
		t.Errorf("unlocked device: unexpected error: %v", err)
	}

	if _, err := l.Lock(testDevice, ownerA, time.Minute); err != nil {
		t.Fatalf("lock: %v", err)
	}

	// Same owner has access.
	if err := l.CheckAccess(testDevice, ownerA); err != nil {
		t.Errorf("same owner: unexpected error: %v", err)
	}

	// Different owner is rejected.
	err := l.CheckAccess(testDevice, ownerB)
	if err == nil {
		t.Fatal("different owner: expected error")
	}

	if !errors.Is(err, dutagent.ErrLocked) {
		t.Errorf("expected ErrLocked, got %v", err)
	}
}

func TestLocker_ConcurrentAccess(t *testing.T) {
	l := dutagent.NewLocker()

	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(i int) {
			defer wg.Done()

			owner := ownerA
			if i%2 == 0 {
				owner = ownerB
			}

			_, _ = l.Lock(testDevice, owner, time.Millisecond)
			_ = l.Unlock(testDevice, owner)
			_, _ = l.Status(testDevice)
			_ = l.CheckAccess(testDevice, owner)
		}(i)
	}

	wg.Wait()
}
