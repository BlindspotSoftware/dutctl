// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/dutagent/locker"
)

// startDrain runs drain in the background and returns a channel that is closed
// when it returns.
func startDrain(lk *locker.Locker, timeout time.Duration, force <-chan os.Signal) <-chan struct{} {
	done := make(chan struct{})

	go func() {
		drain(context.Background(), lk, timeout, force)
		close(done)
	}()

	return done
}

func waitDone(t *testing.T, done <-chan struct{}, within time.Duration, what string) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(within):
		t.Fatalf("drain still waiting after %v, want it to return: %s", within, what)
	}
}

func stillRunning(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()

	select {
	case <-done:
		t.Fatalf("drain returned early: %s", what)
	case <-time.After(3 * drainPollInterval):
	}
}

func TestDrainIdleAgentStopsAtOnce(t *testing.T) {
	waitDone(t, startDrain(locker.New(), 0, nil), 100*time.Millisecond, "no job is running")
}

func TestDrainWaitsUntilJobReleasesReservation(t *testing.T) {
	lk := locker.New()

	if _, err := lk.Lock("dev", "fwci-job", time.Hour); err != nil {
		t.Fatal(err)
	}

	done := startDrain(lk, 0, nil)
	stillRunning(t, done, "the job still holds its reservation")

	// The job goes on while draining: it runs its next command, then ends.
	if _, err := lk.AutoLock("dev", "fwci-job"); err != nil {
		t.Fatalf("job command while draining: %v", err)
	}

	if err := lk.ClearAutoLock("dev", "fwci-job"); err != nil {
		t.Fatal(err)
	}

	stillRunning(t, done, "the job has not released its reservation yet")

	if err := lk.ClearLock("dev", "fwci-job"); err != nil {
		t.Fatal(err)
	}

	waitDone(t, done, time.Second, "the job released its reservation")
}

func TestDrainTakesNoNewWork(t *testing.T) {
	lk := locker.New()

	if _, err := lk.Lock("dev", "fwci-job", time.Hour); err != nil {
		t.Fatal(err)
	}

	done := startDrain(lk, 0, nil)
	stillRunning(t, done, "the job still holds its reservation")

	if _, err := lk.Lock("other", "next-job", time.Hour); !errors.Is(err, locker.ErrDraining) {
		t.Errorf("new reservation while draining: err = %v, want ErrDraining", err)
	}

	if _, err := lk.AutoLock("other", "alice"); !errors.Is(err, locker.ErrDraining) {
		t.Errorf("command on an unreserved device while draining: err = %v, want ErrDraining", err)
	}

	if err := lk.ClearLock("dev", "fwci-job"); err != nil {
		t.Fatal(err)
	}

	waitDone(t, done, time.Second, "the job released its reservation")
}

func TestDrainEndsWhenReservationExpires(t *testing.T) {
	lk := locker.New()

	if _, err := lk.Lock("dev", "forgotten", 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	waitDone(t, startDrain(lk, 0, nil), 2*time.Second, "the reservation expired")
}

func TestDrainSecondSignalStopsAtOnce(t *testing.T) {
	lk := locker.New()

	if _, err := lk.Lock("dev", "fwci-job", time.Hour); err != nil {
		t.Fatal(err)
	}

	force := make(chan os.Signal, 1)
	done := startDrain(lk, 0, force)
	stillRunning(t, done, "the job still holds its reservation")

	force <- syscall.SIGTERM

	waitDone(t, done, 100*time.Millisecond, "a second signal arrived")
}

func TestDrainStopsAfterTimeout(t *testing.T) {
	lk := locker.New()

	if _, err := lk.Lock("dev", "fwci-job", time.Hour); err != nil {
		t.Fatal(err)
	}

	start := time.Now()

	waitDone(t, startDrain(lk, 200*time.Millisecond, nil), 2*time.Second, "the drain timeout elapsed")

	if took := time.Since(start); took < 200*time.Millisecond {
		t.Errorf("drain returned after %v, before its 200ms timeout", took)
	}
}
