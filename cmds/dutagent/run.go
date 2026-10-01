// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"

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
	// stream is quiet.
	defer clearAutoLock(ctx, a.locker, device, user)

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
// another owner has reserved or is running a command on is rejected here,
// atomically with the acquire.
//
// Errors: CodeFailedPrecondition when another owner holds the device
// (locker.ErrWrongOwner); CodeInternal otherwise.
func acquireAutoLock(lk *locker.Locker, device, user string) error {
	_, err := lk.AutoLock(device, user)
	if err != nil {
		if errors.Is(err, locker.ErrWrongOwner) {
			return connect.NewError(connect.CodeFailedPrecondition, err)
		}

		return connect.NewError(connect.CodeInternal, err)
	}

	return nil
}

// clearAutoLock releases the command-scoped auto-lock for device held by user.
// It never touches the explicit lock slot, so an explicit Lock the same owner
// holds for the device survives the run. ErrNotLocked is tolerated: a forced
// unlock no longer wipes the slot, but a run of the same owner that shared the
// hold may already have released it. Any other failure is logged rather than
// returned, as this runs during Run teardown (including panic unwinding), where
// no caller is left to handle it.
func clearAutoLock(ctx context.Context, lk *locker.Locker, device, user string) {
	err := lk.ClearAutoLock(device, user)
	if err != nil && !errors.Is(err, locker.ErrNotLocked) {
		log.FromContext(ctx).Warn("failed to release auto-lock", "device", device, "err", err)
	}
}

// runInSession runs mods, each with its resolved args, in a session: a
// session.Broker carries their I/O over stream while they run. It returns once
// the modules have finished and the broker's workers have stopped, or at the
// first failure.
//
// Errors: see waitModules.
func runInSession(ctx context.Context, stream session.Stream, mods []dut.Module, moduleArgs [][]string) error {
	// The modules run under runCtx; the workers under a child of it, so the
	// workers can be stopped once the modules are done without cancelling the
	// modules' context.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	workerCtx, stopWorkers := context.WithCancel(runCtx)

	var broker session.Broker

	// A worker still inside a stream Send when the handler returns writes to a
	// response writer connect has invalidated, and panics in a goroutine no
	// recover covers. So on every exit path, stop the workers and wait for them.
	defer broker.Wait()
	defer stopWorkers()

	sess, brokerErrCh := broker.Start(workerCtx, stream)

	// The modules run in a goroutine so a failing stream ends the run without
	// waiting for them. The channel is buffered for its single result, so the
	// goroutine never blocks on it once runInSession has returned.
	moduleErrCh := make(chan error, 1)

	go func() {
		defer stopWorkers() // the modules are done: nothing more to carry

		moduleErrCh <- runModules(runCtx, sess, mods, moduleArgs)
	}()

	return waitModules(ctx, moduleErrCh, brokerErrCh)
}

// runModules runs mods in order with their resolved args, stopping at the first
// failure or once ctx is done.
func runModules(ctx context.Context, sess module.Session, mods []dut.Module, moduleArgs [][]string) error {
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

		err := catchPanic(func() error { return mod.Run(modCtx, sess, moduleArgs[idx]...) })
		if err != nil {
			// Deliberate detail+summary logging (not log-and-return spam): this
			// agent-scope line records which module failed (name/index/total) —
			// metadata lost once moduleError flattens the error with %v and Run
			// logs the rpc-scope summary.
			mlog.Error("module failed", "err", err)

			return err
		}
	}

	l.Info("all modules finished successfully")

	return nil
}

// waitModules waits until the modules have finished and the broker's workers
// have stopped, and returns early at the first failure or cancellation.
// moduleErrCh carries the modules' single result; brokerErrCh carries worker
// errors only and is closed once both workers have stopped. A source that has
// finished is set to nil, which disables its select case: a closed channel
// would be selected again on every iteration.
//
// Errors: CodeCanceled/CodeDeadlineExceeded on context cancellation (via
// cancelCode); a module failure via moduleError; a broker failure via
// brokerError.
func waitModules(ctx context.Context, moduleErrCh, brokerErrCh <-chan error) error {
	for moduleErrCh != nil || brokerErrCh != nil {
		select {
		case <-ctx.Done():
			return connect.NewError(cancelCode(ctx.Err()), fmt.Errorf("module execution aborted: %v", ctx.Err()))

		case err := <-moduleErrCh:
			if err != nil {
				return moduleError(err)
			}

			moduleErrCh = nil

		case err, ok := <-brokerErrCh:
			if ok {
				return brokerError(err)
			}

			brokerErrCh = nil
		}
	}

	return nil
}
