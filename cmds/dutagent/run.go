// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/locker"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/session"
	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/pkg/module"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// run carries out a Run request for user over stream: it receives the command,
// resolves it, takes the device's auto-lock and runs the command's modules. It
// is Run minus the connect transport and the caller lookup, so tests can drive
// the whole request with a fake stream. Every error it returns is a
// *connect.Error.
func (a *rpcService) run(ctx context.Context, stream session.Stream, user string) error {
	cmdMsg, err := receiveCommand(stream)
	if err != nil {
		return err
	}

	device, command := cmdMsg.GetDevice(), cmdMsg.GetCommand()

	cmd, err := findCommand(a.devices, device, command)
	if err != nil {
		return err
	}

	// Resolve the arguments before taking the lock: a request that cannot run
	// must not make the device busy, not even briefly.
	moduleArgs, err := cmd.ModuleArgs(cmdMsg.GetArgs())
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	err = acquireAutoLock(a.locker, device, user)
	if err != nil {
		return err
	}

	// Deferred, so the device is handed on however the run ends, a panic
	// included. It runs once runInSession has returned, that is, once the
	// modules have returned and the stream is quiet.
	defer clearAutoLock(ctx, a.locker, device, user)

	// Besides its client, the agent can end the run: it aborts every running
	// command in the second stop stage.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	//nolint:contextcheck // a.aborting is the agent's stop stage, deliberately not derived from the request
	stopAbort := context.AfterFunc(a.aborting, func() { cancel(context.Cause(a.aborting)) })
	defer stopAbort()

	// Module execution is the agent's core orchestration: scope it "agent" and
	// tag the device and command, which then descend to every record on this path.
	ctx = log.With(log.WithScope(ctx, "agent"), "device", device, "command", command)

	return runInSession(ctx, stream, cmd.Modules, moduleArgs)
}

// receiveCommand receives the first message of a Run, which must carry the
// command to run.
//
// Errors: a failed receive maps via receiveError (CodeCanceled or
// CodeDeadlineExceeded on cancellation, CodeAborted otherwise);
// CodeInvalidArgument if the first message is not a command.
func receiveCommand(stream session.Stream) (*pb.Command, error) {
	req, err := stream.Receive()
	if err != nil {
		return nil, receiveError(err)
	}

	cmdMsg := req.GetCommand()
	if cmdMsg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("first run request must contain a command"))
	}

	return cmdMsg, nil
}

// findCommand looks up command on device.
//
// Errors: CodeNotFound for an unknown device or command (dut.ErrDeviceNotFound /
// dut.ErrCommandNotFound); CodeInternal otherwise (the defensive ErrNoModules /
// ErrMultiplePassthroughModules, unreachable after config load).
func findCommand(devices dut.Devlist, device, command string) (dut.Command, error) {
	_, cmd, err := devices.FindCmd(device, command)
	if err != nil {
		code := connect.CodeInternal
		if errors.Is(err, dut.ErrDeviceNotFound) || errors.Is(err, dut.ErrCommandNotFound) {
			code = connect.CodeNotFound
		}

		return dut.Command{}, connect.NewError(code, fmt.Errorf("device %q, command %q: %w", device, command, err))
	}

	return cmd, nil
}

// acquireAutoLock takes the command-scoped auto-lock on device for user.
// AutoLock checks both lock slots under the locker's mutex, so a device that
// another owner has reserved, or that is already running a command for anyone,
// user included, is rejected here, atomically with the acquire.
//
// Errors: CodeFailedPrecondition when another owner holds the device
// (locker.ErrWrongOwner), the device is already running a command for user
// (locker.ErrAlreadyRunning) or the agent is shutting down
// (locker.ErrShuttingDown); CodeInternal otherwise.
func acquireAutoLock(lk *locker.Locker, device, user string) error {
	_, err := lk.AutoLock(device, user)
	if err != nil {
		// One code for all three: either way the device is busy until its
		// state changes, and the message tells the client which case it is.
		if errors.Is(err, locker.ErrWrongOwner) || errors.Is(err, locker.ErrAlreadyRunning) ||
			errors.Is(err, locker.ErrShuttingDown) {
			return connect.NewError(connect.CodeFailedPrecondition, err)
		}

		return connect.NewError(connect.CodeInternal, err)
	}

	return nil
}

// clearAutoLock releases the command-scoped auto-lock for device held by user.
// It never touches the explicit lock slot, so an explicit Lock the same owner
// holds for the device survives the run. Only this run holds the auto-lock (a
// device runs one command at a time), and a forced unlock leaves it in place,
// so the run's hold is still there to release and any failure is unexpected.
// It is logged as a warning rather than returned, as this runs during Run
// teardown (including panic unwinding), where no caller is left to handle it.
func clearAutoLock(ctx context.Context, lk *locker.Locker, device, user string) {
	err := lk.ClearAutoLock(device, user)
	if err != nil {
		log.FromContext(ctx).Warn("failed to release auto-lock", "device", device, "err", err)
	}
}

// runInSession runs mods, each with its resolved args, in a session: a
// session.Broker carries their I/O over stream while they run. The modules run
// on the calling goroutine, so it returns only once they have: the device stays
// busy for as long as a module drives it, even one slow to stop after a
// cancellation. It also returns only once the broker's workers have stopped.
//
// Errors: when ctx is done, via cancelError (CodeAborted if the agent aborted
// the run, otherwise CodeCanceled/CodeDeadlineExceeded); a failed stream or a
// client protocol violation via brokerError; a module failure via moduleError.
func runInSession(ctx context.Context, stream session.Stream, mods []dut.Module, moduleArgs [][]string) error {
	var broker session.Broker

	// sessCtx is cancelled when a worker fails: the stream is broken, so the
	// modules must stop.
	sess, sessCtx := broker.Start(ctx, stream)

	// Stopping the broker waits for its workers. The deferred Stop covers the
	// panic path, where its error has no one to go to: a worker still inside a
	// stream Send when the handler returns writes to a response writer connect
	// has invalidated, and panics in a goroutine no recover covers. Stop is
	// idempotent, so on the normal path it is a no-op after the one below.
	defer func() { _ = broker.Stop() }()

	modErr := runModules(sessCtx, sess, broker.CloseConsole, mods, moduleArgs)
	brokerErr := broker.Stop()

	switch {
	case ctx.Err() != nil:
		return cancelError(ctx)
	case brokerErr != nil:
		// A worker failure is reported over a module error: it cancelled the
		// modules' context and closed their session, so a module error is
		// usually its fallout.
		return brokerError(brokerErr)
	case modErr != nil:
		return moduleError(modErr)
	default:
		return nil
	}
}

// runModules runs mods in order with their resolved args, stopping at the first
// failure or once ctx is done. closeConsole ends the console a module has open;
// it is called once the module returned, however it returned.
func runModules(
	ctx context.Context, sess module.Session, closeConsole func(), mods []dut.Module, moduleArgs [][]string,
) error {
	l := log.FromContext(ctx)
	cnt := len(mods)

	for idx, mod := range mods {
		if ctx.Err() != nil {
			l.Warn("execution aborted", "modules-done", idx, "modules-total", cnt, "err", ctx.Err())

			return ctx.Err()
		}

		// Announce the hand-off in the agent scope (this line is the
		// framework's, not the module's).
		mlog := l.With("module", mod.Config.Name, "module-index", idx+1, "modules-total", cnt)
		mlog.Info("running module")

		// Set the "module" scope on the context handed to the module, so
		// only the module's own records are scoped to it.
		modCtx := log.With(log.WithScope(ctx, "module"), "module", mod.Config.Name, "module-index", idx+1)

		returned := warnUntilReturned(modCtx, mlog, stopWarnInterval)
		err := catchPanic(func() error {
			// The console ends with the module's Run, on a panic too: the
			// defer runs inside the frame catchPanic recovers.
			defer closeConsole()

			return mod.Run(modCtx, sess, moduleArgs[idx]...)
		})

		returned()

		if err != nil {
			// Deliberate detail+summary logging (not log-and-return spam): this
			// agent-scope line records which module failed (name/index/total) —
			// metadata lost once moduleError flattens the error with %v and Run
			// logs the rpc-scope summary. A module that stopped because its command
			// was cancelled did not fail on its own: its error is the fallout.
			if ctx.Err() != nil {
				mlog.Warn("module stopped after the command was cancelled", "err", err, "cause", context.Cause(ctx))
			} else {
				mlog.Error("module failed", "err", err)
			}

			return err
		}
	}

	l.Info("all modules finished successfully")

	return nil
}

// stopWarnInterval is how often the agent warns about a module that has not
// returned since the command was cancelled.
const stopWarnInterval = 10 * time.Second

// warnUntilReturned logs a warning every interval from the moment ctx is done
// until the returned function is called, which the caller does once the module
// has returned. A cancelled module keeps its device busy until it returns, and
// nothing else would say so; if it warned, it also logs when the module finally
// returns. The returned function waits for the warnings to stop, so none is
// logged after it.
func warnUntilReturned(ctx context.Context, l *slog.Logger, interval time.Duration) func() {
	done, exited := make(chan struct{}), make(chan struct{})

	stop := context.AfterFunc(ctx, func() {
		defer close(exited)

		cancelled := time.Now()
		ticker := time.NewTicker(interval)

		defer ticker.Stop()

		warned := false

		for {
			select {
			case <-done:
				if warned {
					l.Warn("module returned after the command was cancelled", "after", time.Since(cancelled).Round(time.Second))
				}

				return
			case <-ticker.C:
				warned = true

				l.Warn("module still running after the command was cancelled; the device stays busy until it returns",
					"waiting", time.Since(cancelled).Round(time.Second), "cause", context.Cause(ctx))
			}
		}
	})

	return func() {
		close(done)

		if !stop() {
			<-exited // the warnings had started
		}
	}
}
