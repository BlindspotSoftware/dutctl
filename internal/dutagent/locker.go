// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dutagent

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrLocked is returned when a device is locked by a different owner.
var ErrLocked = errors.New("device is locked")

// ErrNotLocked is returned when trying to unlock a device that is not locked.
var ErrNotLocked = errors.New("device is not locked")

// ErrWrongOwner is returned when trying to unlock a device locked by a different owner.
var ErrWrongOwner = errors.New("device is locked by a different owner")

// LockInfo holds the lock state for a single device.
type LockInfo struct {
	Owner     string
	LockedAt  time.Time
	ExpiresAt time.Time
}

// LockedError is returned when a lock operation fails because the device is held by another owner.
type LockedError struct {
	Device string
	Info   LockInfo
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("device %q is locked by %q since %s (expires %s)",
		e.Device,
		e.Info.Owner,
		e.Info.LockedAt.UTC().Format(time.RFC3339),
		e.Info.ExpiresAt.UTC().Format(time.RFC3339),
	)
}

func (e *LockedError) Unwrap() error { return ErrLocked }

// Locker manages per-device exclusive locks with time-based expiry.
// Locks do not survive process restarts. Expired locks are pruned lazily on access.
type Locker struct {
	mu    sync.Mutex
	locks map[string]*LockInfo
}

// NewLocker creates a new Locker.
func NewLocker() *Locker {
	return &Locker{
		locks: make(map[string]*LockInfo),
	}
}

// Lock acquires an exclusive lock on device for owner with the given duration.
// If the device is already locked by the same owner, the lock is extended.
// If locked by a different owner, a *LockedError is returned.
func (l *Locker) Lock(device, owner string, duration time.Duration) (LockInfo, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	if existing, ok := l.locks[device]; ok && now.Before(existing.ExpiresAt) {
		// Active lock exists.
		if existing.Owner != owner {
			return LockInfo{}, &LockedError{Device: device, Info: *existing}
		}

		// Same owner — extend the lock without reducing the current expiry.
		if newExpiry := now.Add(duration); existing.ExpiresAt.Before(newExpiry) {
			existing.ExpiresAt = newExpiry
		}

		return *existing, nil
	}

	// No lock or expired — acquire new lock.
	info := &LockInfo{
		Owner:     owner,
		LockedAt:  now,
		ExpiresAt: now.Add(duration),
	}
	l.locks[device] = info

	return *info, nil
}

// Unlock releases the lock on device. Only the owner who holds the lock may unlock it.
// Returns ErrNotLocked if the device is not locked (or the lock has expired),
// and ErrWrongOwner if a different owner tries to unlock.
func (l *Locker) Unlock(device, owner string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	existing, ok := l.locks[device]
	if !ok || now.After(existing.ExpiresAt) {
		delete(l.locks, device)

		return ErrNotLocked
	}

	if existing.Owner != owner {
		return fmt.Errorf("%w: held by %q", ErrWrongOwner, existing.Owner)
	}

	delete(l.locks, device)

	return nil
}

// Status returns the lock info for device and true if the device is currently locked.
// Returns nil, false if the device is unlocked or its lock has expired.
func (l *Locker) Status(device string) (*LockInfo, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.status(device)
}

// status is the unlocked variant of Status.
func (l *Locker) status(device string) (*LockInfo, bool) {
	now := time.Now()

	info, ok := l.locks[device]
	if !ok {
		return nil, false
	}

	if now.After(info.ExpiresAt) {
		delete(l.locks, device)

		return nil, false
	}

	cp := *info

	return &cp, true
}

// StatusAll returns a snapshot of all currently active locks, keyed by device name.
// Expired locks are pruned during this call.
func (l *Locker) StatusAll() map[string]LockInfo {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	result := make(map[string]LockInfo)

	for device, info := range l.locks {
		if now.After(info.ExpiresAt) {
			delete(l.locks, device)

			continue
		}

		result[device] = *info
	}

	return result
}

// CheckAccess returns nil if the device is unlocked (or lock has expired) or if
// the active lock is held by owner. Returns *LockedError if a different owner holds the lock.
func (l *Locker) CheckAccess(device, owner string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	info, locked := l.status(device)
	if !locked {
		return nil
	}

	if info.Owner == owner {
		return nil
	}

	return &LockedError{Device: device, Info: *info}
}
