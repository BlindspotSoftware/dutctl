// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dutagent

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// Sentinel errors returned by Locker.
var (
	// ErrNotLocked is returned when releasing a slot that is not held.
	ErrNotLocked = errors.New("device is not locked")
	// ErrWrongOwner is returned when a non-owner tries to release a slot.
	ErrWrongOwner = errors.New("device is locked by another owner")
	// ErrInvalidDuration is returned when Lock is called with a non-positive
	// duration. Explicit locks always require a positive expiry; the
	// no-expiry semantic is reserved for the auto-lock slot.
	ErrInvalidDuration = errors.New("lock duration must be positive")
)

// Slot identifies which of a device's two lock slots a LockInfo refers to.
type Slot string

const (
	ExplicitSlot Slot = "explicit"
	AutoSlot     Slot = "auto"
)

// LockInfo describes the state of a single lock slot.
type LockInfo struct {
	Owner    string
	LockedAt time.Time
	// ExpiresAt is the time the lock expires. The zero value means the lock
	// never expires by time; only auto-locks may carry a zero ExpiresAt.
	ExpiresAt time.Time
	// Slot reports which slot this LockInfo was read from. Set by the
	// Locker on every value it produces.
	Slot Slot
}

// isExpired reports whether a slot has a time-based expiry that has passed.
// A zero ExpiresAt never expires.
func (li LockInfo) isExpired(now time.Time) bool {
	return !li.ExpiresAt.IsZero() && !now.Before(li.ExpiresAt)
}

// DeviceLockState is a snapshot of both slot states for a single device.
// Each pointer is nil when the corresponding slot is empty.
type DeviceLockState struct {
	Explicit *LockInfo
	Auto     *LockInfo
}

// LockError is returned when an operation is denied because the device is
// held by a different owner. Holder is the LockInfo of the lock that blocks
// the operation (its owner, when it was taken, its expiry, and which slot
// it lives in via Holder.Slot). LockError unwraps to ErrWrongOwner so
// callers can match the "different owner" case across acquire (Lock/AutoLock)
// and release (ClearLock/ClearAutoLock) APIs with a single errors.Is check.
type LockError struct {
	Device string
	Holder LockInfo
}

// humanRemaining renders dur as a compact "1h30m"-style string, rounded to
// the minute. Non-positive durations render as "0m".
func humanRemaining(dur time.Duration) string {
	if dur <= 0 {
		return "0m"
	}

	dur = dur.Round(time.Minute)

	hours := dur / time.Hour
	minutes := (dur % time.Hour) / time.Minute

	switch {
	case hours > 0 && minutes > 0:
		return fmt.Sprintf("%dh%dm", hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

func (e *LockError) Error() string {
	if e.Holder.Slot == ExplicitSlot {
		remaining := humanRemaining(time.Until(e.Holder.ExpiresAt))

		return fmt.Sprintf("device %q is locked by %q for %s", e.Device, e.Holder.Owner, remaining)
	}

	return fmt.Sprintf("device %q is locked by %q", e.Device, e.Holder.Owner)
}

func (e *LockError) Unwrap() error {
	return ErrWrongOwner
}

// Locker tracks per-device locks with two independent slots: an explicit
// slot driven by Lock/ClearLock/ForceClearLock and an auto slot driven by
// AutoLock/ClearAutoLock. The two slots are stored separately so a normal
// clear of one never affects the other. ForceClearLock is the one exception:
// it is an admin escape hatch that wipes both slots. Locker is safe for
// concurrent use. Lock state is held in memory only and is lost on agent
// restart.
type Locker struct {
	mu       sync.Mutex
	explicit map[string]LockInfo
	auto     map[string]LockInfo
}

// NewLocker returns a ready-to-use Locker.
func NewLocker() *Locker {
	return &Locker{
		explicit: make(map[string]LockInfo),
		auto:     make(map[string]LockInfo),
	}
}

// hasExplicitLock returns the live explicit-slot lock for device, pruning it
// first if it has expired. The caller must hold l.mu.
func (l *Locker) hasExplicitLock(device string) (LockInfo, bool) {
	info, ok := l.explicit[device]
	if !ok {
		return LockInfo{}, false
	}

	if info.isExpired(time.Now()) {
		delete(l.explicit, device)

		return LockInfo{}, false
	}

	return info, true
}

// checkLocked returns a *LockError describing whichever slot would block
// owner from operating on device, or nil if owner has access. The caller
// must hold l.mu.
func (l *Locker) checkLocked(device, owner string) *LockError {
	if info, held := l.hasExplicitLock(device); held && info.Owner != owner {
		return &LockError{Device: device, Holder: info}
	}

	if info, held := l.auto[device]; held && info.Owner != owner {
		return &LockError{Device: device, Holder: info}
	}

	return nil
}

// Lock acquires the explicit-slot lock on device for owner. dur must be
// positive; ErrInvalidDuration is returned otherwise. If the device is
// already explicit-locked by the same owner, the lock is extended: the new
// expiry is the later of the current and now+dur. If either slot is held by
// a different owner, a *LockError is returned.
func (l *Locker) Lock(device, owner string, dur time.Duration) (LockInfo, error) {
	if dur <= 0 {
		return LockInfo{}, ErrInvalidDuration
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	blocker := l.checkLocked(device, owner)
	if blocker != nil {
		return LockInfo{}, blocker
	}

	now := time.Now()
	newExpiry := now.Add(dur)

	if existing, held := l.hasExplicitLock(device); held {
		// Same-owner re-lock extends but never shrinks the expiry.
		updated := existing
		if newExpiry.After(existing.ExpiresAt) {
			updated.ExpiresAt = newExpiry
		}

		l.explicit[device] = updated

		return updated, nil
	}

	info := LockInfo{Owner: owner, LockedAt: now, ExpiresAt: newExpiry, Slot: ExplicitSlot}
	l.explicit[device] = info

	return info, nil
}

// ClearLock releases the explicit-slot lock on device. Only the owner may
// release it. ErrNotLocked / *LockError as appropriate. The auto slot is
// not touched.
func (l *Locker) ClearLock(device, owner string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	info, ok := l.hasExplicitLock(device)
	if !ok {
		return ErrNotLocked
	}

	if info.Owner != owner {
		return &LockError{Device: device, Holder: info}
	}

	delete(l.explicit, device)

	return nil
}

// ForceClearLock releases both slots on device regardless of owner. As an
// admin escape hatch, it intentionally wipes any auto-lock as well so a
// stuck command holder cannot survive a forced unlock. Returns ErrNotLocked
// only when neither slot was held.
func (l *Locker) ForceClearLock(device string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	explicitInfo, hadExplicit := l.hasExplicitLock(device)
	autoInfo, hadAuto := l.auto[device]

	if !hadExplicit && !hadAuto {
		return ErrNotLocked
	}

	if hadExplicit {
		log.Printf("Force-clearing explicit lock on device %q, previously held by %q", device, explicitInfo.Owner)
		delete(l.explicit, device)
	}

	if hadAuto {
		log.Printf("Force-clearing auto lock on device %q, previously held by %q", device, autoInfo.Owner)
		delete(l.auto, device)
	}

	return nil
}

// AutoLock acquires the auto-slot lock on device for owner. Auto locks carry
// no expiry. Re-AutoLock by the same owner is a no-op. If either slot is
// held by a different owner, a *LockError is returned.
func (l *Locker) AutoLock(device, owner string) (LockInfo, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	blocker := l.checkLocked(device, owner)
	if blocker != nil {
		return LockInfo{}, blocker
	}

	if existing, held := l.auto[device]; held {
		return existing, nil
	}

	info := LockInfo{Owner: owner, LockedAt: time.Now(), Slot: AutoSlot}
	l.auto[device] = info

	return info, nil
}

// ClearAutoLock releases the auto-slot lock on device. Only the owner may
// release it. ErrNotLocked / *LockError as appropriate. The explicit slot
// is not touched.
func (l *Locker) ClearAutoLock(device, owner string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	info, ok := l.auto[device]
	if !ok {
		return ErrNotLocked
	}

	if info.Owner != owner {
		return &LockError{Device: device, Holder: info}
	}

	delete(l.auto, device)

	return nil
}

// CheckAccess reports whether owner may operate on device. It returns nil if
// neither slot is held or if every held slot is owned by owner; otherwise it
// returns a *LockError carrying the blocking slot's holder.
func (l *Locker) CheckAccess(device, owner string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	blocker := l.checkLocked(device, owner)
	if blocker != nil {
		return blocker
	}

	return nil
}

// StatusAll returns a snapshot of both slot states for every device that has
// at least one slot held. Expired explicit slots are pruned and not included.
func (l *Locker) StatusAll() map[string]DeviceLockState {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make(map[string]DeviceLockState)

	for device := range l.explicit {
		if info, ok := l.hasExplicitLock(device); ok {
			state := out[device]
			state.Explicit = &LockInfo{Owner: info.Owner, LockedAt: info.LockedAt, ExpiresAt: info.ExpiresAt, Slot: ExplicitSlot}
			out[device] = state
		}
	}

	for device, info := range l.auto {
		state := out[device]
		state.Auto = &LockInfo{Owner: info.Owner, LockedAt: info.LockedAt, ExpiresAt: info.ExpiresAt, Slot: AutoSlot}
		out[device] = state
	}

	return out
}
