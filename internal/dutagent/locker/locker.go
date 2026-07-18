// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package locker

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/log"
)

// Sentinel errors returned by Locker.
var (
	// ErrNotLocked is returned when releasing a hold that is not held.
	ErrNotLocked = errors.New("device is not locked")
	// ErrWrongOwner is returned when a non-owner tries to release a hold.
	ErrWrongOwner = errors.New("device is locked by another owner")
	// ErrInvalidDuration is returned when Lock is called with a non-positive
	// duration. A reservation always requires a positive expiry; the no-expiry
	// semantic belongs to a Busy hold.
	ErrInvalidDuration = errors.New("lock duration must be positive")
)

// Kind is the sort of hold a device carries.
type Kind int

const (
	// Reserved is an explicit, user-requested lock with a time bound (the
	// `lock` command). It carries the device's meaningful expiry.
	Reserved Kind = iota
	// Busy is a transient hold taken automatically while a command runs. It
	// has no time expiry and is released when the command finishes.
	Busy
)

// String renders a Kind for logs and diagnostics.
func (k Kind) String() string {
	switch k {
	case Reserved:
		return "reserved"
	case Busy:
		return "busy"
	default:
		return "unknown"
	}
}

// Hold describes a single hold on a device: who owns it, when it was taken,
// when it expires (the zero value for a Busy hold, which never expires by
// time), and its Kind. The Locker sets Kind on every Hold it produces.
type Hold struct {
	Owner     string
	LockedAt  time.Time
	ExpiresAt time.Time
	Kind      Kind
}

// isExpired reports whether a hold has a time-based expiry that has passed.
// A zero ExpiresAt never expires.
func (h Hold) isExpired(now time.Time) bool {
	return !h.ExpiresAt.IsZero() && !now.Before(h.ExpiresAt)
}

// Error is returned when an operation is denied because the device is held by
// a different owner. Holder is the Hold that blocks the operation (its owner,
// when it was taken, its expiry, and its Kind). Error unwraps to ErrWrongOwner
// so callers can match the "different owner" case across acquire (Lock/AutoLock)
// and release (ClearLock/ClearAutoLock) APIs with a single errors.Is check.
//
// It is always returned as a pointer (*Error) and uses a pointer receiver:
// match the category with errors.Is(err, ErrWrongOwner) and reach the Holder
// metadata with errors.As into a *Error.
type Error struct {
	Device string
	Holder Hold
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

func (e *Error) Error() string {
	if e.Holder.Kind == Reserved {
		remaining := humanRemaining(time.Until(e.Holder.ExpiresAt))

		return fmt.Sprintf("device %q is locked by %q for %s", e.Device, e.Holder.Owner, remaining)
	}

	return fmt.Sprintf("device %q is locked by %q", e.Device, e.Holder.Owner)
}

func (e *Error) Unwrap() error {
	return ErrWrongOwner
}

// Locker tracks per-device holds of two kinds: a Reserved hold driven by
// Lock/ClearLock/ForceClearLock and a Busy hold driven by AutoLock/
// ClearAutoLock. The two are stored separately so a normal clear of one never
// affects the other. ForceClearLock is the one exception: it is an admin
// escape hatch that clears both. Locker is safe for concurrent use. Hold state
// is held in memory only and is lost on agent restart.
type Locker struct {
	mu sync.Mutex
	// reserved holds Reserved-kind holds (the `lock` command); busy holds
	// Busy-kind holds taken automatically while a command runs.
	reserved map[string]Hold
	busy     map[string]Hold
	log      *slog.Logger
}

// New returns a ready-to-use Locker.
func New() *Locker {
	return &Locker{
		reserved: make(map[string]Hold),
		busy:     make(map[string]Hold),
		log:      log.Scope(slog.Default(), "locker"),
	}
}

// liveReservation returns the live Reserved hold for device, pruning it first
// if it has expired. The caller must hold l.mu.
func (l *Locker) liveReservation(device string) (Hold, bool) {
	hold, ok := l.reserved[device]
	if !ok {
		return Hold{}, false
	}

	if hold.isExpired(time.Now()) {
		delete(l.reserved, device)
		// The only lifecycle event a caller never drives explicitly: the
		// reservation ends here, lazily, and the device becomes free to others.
		l.log.Info("reservation expired", "device", device, "owner", hold.Owner)

		return Hold{}, false
	}

	return hold, true
}

// checkLocked returns a *Error describing whichever hold would block owner from
// operating on device, or nil if owner has access. The caller must hold l.mu.
func (l *Locker) checkLocked(device, owner string) *Error {
	if hold, held := l.liveReservation(device); held && hold.Owner != owner {
		return &Error{Device: device, Holder: hold}
	}

	if hold, held := l.busy[device]; held && hold.Owner != owner {
		return &Error{Device: device, Holder: hold}
	}

	return nil
}

// Lock acquires the Reserved hold on device for owner. dur must be positive;
// ErrInvalidDuration is returned otherwise. If the device is already reserved
// by the same owner, the reservation is extended: the new expiry is the later
// of the current and now+dur. If either hold is held by a different owner, a
// *Error is returned.
func (l *Locker) Lock(device, owner string, dur time.Duration) (Hold, error) {
	if dur <= 0 {
		return Hold{}, ErrInvalidDuration
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	blocker := l.checkLocked(device, owner)
	if blocker != nil {
		return Hold{}, blocker
	}

	now := time.Now()
	newExpiry := now.Add(dur)

	if existing, held := l.liveReservation(device); held {
		// Same-owner re-lock extends but never shrinks the expiry.
		updated := existing
		if newExpiry.After(existing.ExpiresAt) {
			updated.ExpiresAt = newExpiry
		}

		l.reserved[device] = updated

		return updated, nil
	}

	hold := Hold{Owner: owner, LockedAt: now, ExpiresAt: newExpiry, Kind: Reserved}
	l.reserved[device] = hold

	return hold, nil
}

// ClearLock releases the Reserved hold on device. Only the owner may release
// it: it returns ErrNotLocked when no reservation is held, or a *Error
// (unwrapping to ErrWrongOwner) when a different owner holds it. The Busy hold
// is not touched.
func (l *Locker) ClearLock(device, owner string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	hold, ok := l.liveReservation(device)
	if !ok {
		return ErrNotLocked
	}

	if hold.Owner != owner {
		return &Error{Device: device, Holder: hold}
	}

	delete(l.reserved, device)

	return nil
}

// ForceClearLock releases both holds on device regardless of owner. As an admin
// escape hatch, it intentionally clears any Busy hold as well so a stuck command
// holder cannot survive a forced unlock. Returns ErrNotLocked only when neither
// hold was held.
func (l *Locker) ForceClearLock(device string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	reservation, hadReservation := l.liveReservation(device)
	busyHold, hadBusy := l.busy[device]

	if !hadReservation && !hadBusy {
		return ErrNotLocked
	}

	if hadReservation {
		l.log.Warn("force-clearing hold", "kind", Reserved, "device", device, "previous_owner", reservation.Owner)
		delete(l.reserved, device)
	}

	if hadBusy {
		l.log.Warn("force-clearing hold", "kind", Busy, "device", device, "previous_owner", busyHold.Owner)
		delete(l.busy, device)
	}

	return nil
}

// AutoLock acquires the Busy hold on device for owner. Busy holds carry no
// expiry. Re-acquiring by the same owner is a no-op. If either hold is held by
// a different owner, a *Error is returned.
func (l *Locker) AutoLock(device, owner string) (Hold, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	blocker := l.checkLocked(device, owner)
	if blocker != nil {
		return Hold{}, blocker
	}

	if existing, held := l.busy[device]; held {
		return existing, nil
	}

	hold := Hold{Owner: owner, LockedAt: time.Now(), Kind: Busy}
	l.busy[device] = hold
	l.log.Debug("auto-lock acquired", "device", device, "owner", owner)

	return hold, nil
}

// ClearAutoLock releases the Busy hold on device. Only the owner may release
// it: it returns ErrNotLocked when no Busy hold is held, or a *Error
// (unwrapping to ErrWrongOwner) when a different owner holds it. The Reserved
// hold is not touched.
func (l *Locker) ClearAutoLock(device, owner string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	hold, ok := l.busy[device]
	if !ok {
		return ErrNotLocked
	}

	if hold.Owner != owner {
		return &Error{Device: device, Holder: hold}
	}

	delete(l.busy, device)
	l.log.Debug("auto-lock released", "device", device, "owner", owner)

	return nil
}

// CheckAccess reports whether owner may operate on device. It returns nil if
// neither hold is held or if every held hold is owned by owner; otherwise it
// returns a *Error carrying the blocking holder.
func (l *Locker) CheckAccess(device, owner string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	blocker := l.checkLocked(device, owner)
	if blocker != nil {
		return blocker
	}

	return nil
}

// StatusAll returns the effective hold for every device that currently has one.
// A device with a live reservation reports that Reserved hold (it carries the
// meaningful expiry); a device that is only busy reports its Busy hold. Expired
// reservations are pruned and omitted; free devices are absent.
func (l *Locker) StatusAll() map[string]Hold {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make(map[string]Hold)

	// Busy holds first, then let a live reservation shadow it: when a device
	// has both (necessarily the same owner), the reservation is the meaningful
	// holder to report.
	for device, hold := range l.busy {
		out[device] = hold
	}

	for device := range l.reserved {
		if hold, ok := l.liveReservation(device); ok {
			out[device] = hold
		}
	}

	return out
}
