// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/dutagent/locker"
	"github.com/BlindspotSoftware/dutctl/internal/log"
)

// errAbortedByShutdown is the cause of every run the agent aborts while it stops;
// the run's client sees it in the run's error.
var errAbortedByShutdown = errors.New("dutagent is shutting down")

// stopper takes the agent through its stop stages, one per stop signal:
//
//  1. Drain: take no new work, and let the running commands and the
//     reservations end on their own.
//  2. Abort: abort the running commands and wait until their modules have
//     returned, without a bound.
//  3. Exit at once, without deinitializing the modules.
//
// drain and abort enter the first two stages; the third is the exit in
// watchStops. Each first puts locks into its mode and then ends its stage's
// context: draining is done from the first stage on, aborting from the second,
// with errAbortedByShutdown as its cause. So whoever sees a stage's context
// done finds locks already in that stage.
//
// A failure of the agent itself enters the second stage directly, through
// abortAndWait, which may run beside a stop signal: entering a stage is
// idempotent, so such a signal is at worst counted one stage low, and the
// next one exits. docs/dutagent-shutdown.md shows how the stages play out.
type stopper struct {
	locks *locker.Locker

	draining context.Context //nolint:containedctx // agent-lifetime stop stage, not a request context
	aborting context.Context //nolint:containedctx // agent-lifetime stop stage, not a request context

	cancelDrain context.CancelFunc
	cancelAbort context.CancelCauseFunc
}

// stopStages is the number of stop stages, and of stop signals it takes to go
// through all of them (see stopper).
const stopStages = 3

// notifyStops returns a stopper of locks driven by the process's stop signals:
// SIGTERM from systemd or kill, SIGINT from Ctrl-C, and SIGHUP when the agent's
// terminal goes away. They count together. SIGINT and SIGHUP are left alone if
// the agent was started with them ignored, as by nohup or a script that starts
// it in the background: whoever did so wants it to outlive the terminal.
// SIGQUIT is left to the Go runtime, whose default dumps every goroutine's
// stack and exits: the way to find out why a module does not return. exit is
// called on a stop signal in the second stage. ctx carries the logger. The
// returned function stops relaying signals to the stopper.
func notifyStops(ctx context.Context, locks *locker.Locker, exit func(int)) (*stopper, func()) {
	stops := []os.Signal{syscall.SIGTERM}

	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGHUP} {
		if !signal.Ignored(sig) {
			stops = append(stops, sig)
		}
	}

	// One slot per stage. The OS does not queue signals: identical ones sent in
	// a quick burst may arrive as one.
	sigs := make(chan os.Signal, stopStages)
	signal.Notify(sigs, stops...)

	return watchStops(ctx, sigs, locks, exit), func() { signal.Stop(sigs) }
}

// watchStops returns a stopper of locks that each value received from sigs
// takes from the stage it is in to the next one. Its stage contexts derive
// from ctx.
func watchStops(ctx context.Context, sigs <-chan os.Signal, locks *locker.Locker, exit func(int)) *stopper {
	s := &stopper{locks: locks}
	s.draining, s.cancelDrain = context.WithCancel(ctx)
	s.aborting, s.cancelAbort = context.WithCancelCause(ctx)

	l := log.FromContext(ctx)

	go func() {
		for sig := range sigs {
			switch {
			case s.draining.Err() == nil:
				l.Info("stop signal received: shutting down, taking no new work", "signal", sig)
				s.drain()
			case s.aborting.Err() == nil:
				l.Warn("stop signal received again: aborting the running commands", "signal", sig)
				s.abort()
			default:
				l.Error("stop signal received while aborting: exiting at once, modules are not deinitialized",
					"signal", sig)
				exit(int(exit1))

				return
			}
		}
	}()

	return s
}

// drain enters the first stop stage.
func (s *stopper) drain() {
	s.locks.Drain()
	s.cancelDrain()
}

// abort enters the second stop stage, the first included, and returns at once:
// the locker grants no new hold and ends the reservations, and the running
// commands are cancelled. aborting ends before draining, so whoever wakes on
// draining finds aborting done as well and does not wait for the first stage.
func (s *stopper) abort() {
	s.locks.Close()
	s.cancelAbort(errAbortedByShutdown)
	s.cancelDrain()
}

// abortAndWait enters the second stop stage, unless it has begun already, and
// waits, without a bound, until the aborted commands' modules have returned.
// ctx carries the logger.
func (s *stopper) abortAndWait(ctx context.Context) {
	s.abort()
	waitIdle(ctx, s.locks, nil, "waiting for the aborted commands to stop; send the stop signal again to exit at once")
}

// serve runs the RPC service until the first stop signal, then waits through
// the stop stages until no command runs and nothing is reserved any more, and
// only then stops the service. So once serve has returned, no module runs and
// the caller may deinitialize them. A service that stops on its own, in
// whatever stage, enters the second stage, and serve then returns exit1. ctx
// carries the logger.
func (agt *agent) serve(ctx context.Context, stop *stopper) exitCode {
	l := log.FromContext(ctx)

	service := &rpcService{
		devices:  agt.config.Devices,
		locker:   agt.locks,
		aborting: stop.aborting,
	}

	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()

	failed := make(chan bool, 1)

	go func() {
		err := agt.startRPCService(serveCtx, service)
		if serveCtx.Err() == nil {
			// The service stopped on its own, e.g. because its address is
			// taken. Commands on the connections it accepted may still run.
			l.Error("rpc service stopped", "err", err)
			stop.abort()

			failed <- true

			return
		}

		if err != nil {
			l.Warn("rpc service did not stop cleanly", "err", err)
		}

		failed <- false
	}()

	<-stop.draining.Done()

	// Stage 1 lasts until the locker is empty, unless stage 2 begins first.
	// Once a draining locker is empty, it grants no hold again.
	waitIdle(ctx, agt.locks, stop.aborting.Done(),
		"waiting for the running commands and reservations to end; send the stop signal again to abort the commands")

	if stop.aborting.Err() != nil {
		stop.abortAndWait(ctx)
	}

	// Nothing runs any more; what the service still drains are short requests
	// such as List.
	stopServing()

	if <-failed {
		return exit1
	}

	return exit0
}

// pollInterval is how often a stop stage checks whether the locker is empty.
// Reservations end by time rather than by an event, so the stages poll.
const pollInterval = 250 * time.Millisecond

// waitIdle waits until locks holds nothing, that is, no command runs and
// nothing is reserved, or until giveUp is closed; a nil giveUp never is. While
// it waits, it logs to ctx's logger what it waits for: first with msg, then on
// every change.
func waitIdle(ctx context.Context, locks *locker.Locker, giveUp <-chan struct{}, msg string) {
	select {
	case <-giveUp:
		return
	default:
	}

	waiting := describeHolds(locks.StatusAll())
	if waiting == "" {
		return
	}

	l := log.FromContext(ctx)
	l.Info(msg, "waiting-for", waiting)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-giveUp:
			return
		case <-ticker.C:
		}

		now := describeHolds(locks.StatusAll())
		if now == "" {
			l.Info("nothing left to wait for")

			return
		}

		if now != waiting {
			l.Info("still waiting", "waiting-for", now)

			waiting = now
		}
	}
}

// describeHolds renders holds for the log, sorted by device, such as
// "dev1 (running a command for alice), dev2 (reserved by bob until 14:30)".
// It returns "" for no holds.
func describeHolds(holds map[string]locker.Hold) string {
	out := make([]string, 0, len(holds))

	for device, hold := range holds {
		if hold.Kind == locker.Reserved {
			out = append(out, fmt.Sprintf("%s (reserved by %s until %s)", device, hold.Owner, hold.ExpiresAt.Format("15:04")))
		} else {
			out = append(out, fmt.Sprintf("%s (running a command for %s)", device, hold.Owner))
		}
	}

	slices.Sort(out)

	return strings.Join(out, ", ")
}
