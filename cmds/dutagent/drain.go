// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"os"
	"sort"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/dutagent/locker"
	"github.com/BlindspotSoftware/dutctl/internal/log"
)

// defaultDrainTimeout bounds how long a graceful shutdown waits for running
// jobs. It covers the longest FirmwareCI job, whose reservation lasts the 8h job
// timeout plus 10min margin, so a restart never cuts a CI job short.
const defaultDrainTimeout = 9 * time.Hour

// drainPollInterval is how often a graceful shutdown checks whether the last
// reservation has ended. Expiry happens by time, not by an event, so it polls.
const drainPollInterval = 250 * time.Millisecond

// drain lets running jobs finish before the agent stops, so a package or config
// update can restart it without breaking CI jobs (systemctl restart --no-block).
//
// A job is a device reservation: FirmwareCI reserves the device with Lock for a
// whole job and releases it with Unlock at the end. From the first signal on,
// the locker takes no new work, that is no new reservation and no command on a
// device the caller has not reserved, while existing reservations keep working
// so their jobs can run to the end. drain returns once no reservation is left
// (released or expired), when timeout elapses (0 means no limit), or when a
// second signal on force demands an immediate stop. Commands running without a
// reservation are not waited for here; they get the RPC server's usual grace
// period afterwards, as before. It logs through the logger carried by ctx.
func drain(ctx context.Context, locks *locker.Locker, timeout time.Duration, force <-chan os.Signal) {
	l := log.FromContext(ctx)

	locks.Drain()

	waiting := locks.Reservations()
	if len(waiting) == 0 {
		l.Info("graceful shutdown: no running jobs, stopping now")

		return
	}

	l.Info("graceful shutdown: taking no new work, waiting for running jobs to end; "+
		"send the signal again to stop now", "reservations", describe(waiting), "timeout", timeout)

	var deadline <-chan time.Time

	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()

		deadline = timer.C
	}

	ticker := time.NewTicker(drainPollInterval)
	defer ticker.Stop()

	for {
		select {
		case sig := <-force:
			l.Warn("graceful shutdown: second signal, stopping now", "signal", sig, "reservations", describe(waiting))

			return
		case <-deadline:
			l.Warn("graceful shutdown: timeout reached, stopping with jobs still running",
				"timeout", timeout, "reservations", describe(waiting))

			return
		case <-ticker.C:
			now := locks.Reservations()
			if len(now) == 0 {
				l.Info("graceful shutdown: all jobs ended, stopping now")

				return
			}

			if len(now) != len(waiting) {
				l.Info("graceful shutdown: still waiting for running jobs", "reservations", describe(now))
			}

			waiting = now
		}
	}
}

// describe renders reservations as sorted "device (owner)" entries for the log.
func describe(reservations map[string]locker.Hold) []string {
	out := make([]string, 0, len(reservations))
	for device, hold := range reservations {
		out = append(out, device+" ("+hold.Owner+")")
	}

	sort.Strings(out)

	return out
}
