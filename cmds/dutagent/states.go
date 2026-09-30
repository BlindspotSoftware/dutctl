// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/locker"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/session"
	"github.com/BlindspotSoftware/dutctl/internal/fsm"
	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/pkg/module"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// autoLockHold records the command-scoped auto-lock a Run has acquired, so the
// handler can release it on every exit path — including a panic in a state
// function that unwinds past the FSM before it releases the lock itself. The
// FSM passes runCmdArgs by value, so the record is shared by pointer:
// acquireAutoLock writes it and the deferred release in Run reads it.
type autoLockHold struct {
	device string
	held   bool
}

// runCmdArgs are arguments for the finite state machine in the Run RPC.
type runCmdArgs struct {
	// dependencies of the state machine
	stream     session.Stream
	deviceList dut.Devlist
	locker     *locker.Locker
	user       string
	autoLock   *autoLockHold
	broker     *session.Broker // Lifetime owned by Run, waited on before returning (see Run), started by executeModules

	// fields for the states used during execution
	cmdMsg      *pb.Command
	dev         dut.Device
	cmd         dut.Command
	session     module.Session
	moduleErrCh chan error
	moduleDone  chan struct{} // closed when the module goroutine returns, on every path
	stopGrace   time.Duration // how long a canceled run waits for moduleDone; 0 means defaultModuleStopGrace
	brokerErrCh <-chan error
}

// receiveCommandRPC is the first state of the Run RPC.
//
// It receives a message from the client. As the client could potentially send
// messages of various types, it filters for a command message and returns an
// error otherwise.
//
// Errors: for a failed receive, a context cancellation maps to
// CodeCanceled/CodeDeadlineExceeded (via cancelCode) and anything else is
// CodeAborted; CodeInvalidArgument if the first message is not a command.
func receiveCommandRPC(_ context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	req, err := args.stream.Receive()
	if err != nil {
		return args, nil, receiveError(err)
	}

	cmdMsg := req.GetCommand()
	if cmdMsg == nil {
		e := connect.NewError(connect.CodeInvalidArgument, errors.New("first run request must contain a command"))

		return args, nil, e
	}

	args.cmdMsg = cmdMsg

	return args, findDUTCmd, nil
}

// findDUTCmd is a state of the Run RPC.
//
// It finds the device under test (DUT) by the device name in the command
// message and the command to run by the command name. If the device is not
// found, or the command is not available at the respective device, it returns
// an error.
//
// Errors: CodeNotFound for an unknown device or command
// (dut.ErrDeviceNotFound / dut.ErrCommandNotFound); CodeInternal otherwise (the
// defensive ErrNoModules / ErrMultiplePassthroughModules, unreachable after config load).
func findDUTCmd(_ context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	wantDev := args.cmdMsg.GetDevice()
	wantCmd := args.cmdMsg.GetCommand()

	dev, cmd, err := args.deviceList.FindCmd(wantDev, wantCmd)
	if err != nil {
		var code connect.Code
		if errors.Is(err, dut.ErrDeviceNotFound) || errors.Is(err, dut.ErrCommandNotFound) {
			code = connect.CodeNotFound
		} else {
			code = connect.CodeInternal
		}

		e := connect.NewError(
			code,
			fmt.Errorf("device %q, command %q: %w", wantDev, wantCmd, err),
		)

		return args, nil, e
	}

	args.dev = dev
	args.cmd = cmd

	return args, checkDeviceAccess, nil
}

// checkDeviceAccess is a state of the Run RPC.
//
// It rejects the run if the device is held by a different owner in either
// the explicit or auto lock slot. Otherwise the FSM proceeds to acquire the
// command-scoped auto-lock.
//
// Errors: CodeFailedPrecondition when another owner holds the device
// (locker.ErrWrongOwner); CodeInternal otherwise.
func checkDeviceAccess(_ context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	err := args.locker.CheckAccess(args.cmdMsg.GetDevice(), args.user)
	if err != nil {
		if errors.Is(err, locker.ErrWrongOwner) {
			return args, nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}

		return args, nil, connect.NewError(connect.CodeInternal, err)
	}

	return args, acquireAutoLock, nil
}

// acquireAutoLock is a state of the Run RPC.
//
// It acquires the command-scoped auto-lock for the device. AutoLock is
// idempotent for the same owner, so this is safe even if the same owner
// already holds an auto-lock from a previous race-lost step. On success it
// records the hold on args.autoLock so Run releases it on every exit path.
//
// Errors: CodeFailedPrecondition when another owner holds the device
// (locker.ErrWrongOwner); CodeInternal otherwise.
func acquireAutoLock(_ context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	device := args.cmdMsg.GetDevice()

	_, err := args.locker.AutoLock(device, args.user)
	if err != nil {
		if errors.Is(err, locker.ErrWrongOwner) {
			return args, nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}

		return args, nil, connect.NewError(connect.CodeInternal, err)
	}

	args.autoLock.device = device
	args.autoLock.held = true

	return args, executeModules, nil
}

// runModule runs a single module, recovering a panic into an error so a
// misbehaving module aborts only its run instead of crashing the agent. (The
// session's Console invariant guards also panic).
func runModule(ctx context.Context, mod dut.Module, s module.Session, args ...string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("module panicked: %v", r)
		}
	}()

	return mod.Run(ctx, s, args...)
}

// executeModules is a state of the Run RPC.
//
// It starts the current command's modules in a separate goroutine and does not
// wait for them to finish. It also starts worker goroutines that serve the
// module-to-client communication during module execution.
//
// Errors: CodeInvalidArgument if the command's arguments cannot be resolved
// (see Command.ModuleArgs). Module and broker failures surface later, in waitModules.
func executeModules(ctx context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	// Module execution is the agent's core orchestration: scope it "agent" and
	// tag the device and command, which then descend to every record on this path.
	ctx = log.With(log.WithScope(ctx, "agent"), "device", args.cmdMsg.GetDevice(), "command", args.cmdMsg.GetCommand())

	// Deferred initialization of the moduleErr channel: only create if not already provided
	// (tests may still pass a custom channel).
	if args.moduleErrCh == nil {
		args.moduleErrCh = make(chan error, 1)
	}

	modCtx, modCtxCancel := context.WithCancel(ctx)

	moduleSession, brokerErrCh := args.broker.Start(modCtx, args.stream)
	args.brokerErrCh = brokerErrCh
	args.session = moduleSession

	// Resolve module arguments before spawning the goroutine.
	moduleArgs, err := args.cmd.ModuleArgs(args.cmdMsg.GetArgs())
	if err != nil {
		modCtxCancel()

		return args, nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Run the modules in a goroutine.
	// Termination of the module execution is signaled by closing the moduleErrCh channel.
	args.moduleDone = make(chan struct{})

	go runModules(ctx, args, moduleSession, moduleArgs, modCtxCancel)

	return args, waitModules, nil
}

// runModules runs the command's modules in order for executeModules. It reports
// the outcome on args.moduleErrCh (an error, or the channel closed on success)
// and closes args.moduleDone when it returns, however it ends. cancel stops the
// broker's module context once the modules are done.
func runModules(ctx context.Context, args runCmdArgs, sess module.Session, moduleArgs [][]string, cancel context.CancelFunc) {
	defer close(args.moduleDone)

	l := log.FromContext(ctx)
	cnt := len(args.cmd.Modules)

	for idx, mod := range args.cmd.Modules {
		if ctx.Err() != nil {
			l.Warn("execution aborted", "modules-done", idx, "modules-total", cnt, "err", ctx.Err())
			cancel()

			return
		}

		// Announce the hand-off in the agent scope (this line is the
		// framework's, not the module's).
		mlog := l.With("module", mod.Config.Name, "module-index", idx+1, "modules-total", cnt)
		mlog.Info("running module")

		// Set the "module" scope on the context handed to the module, so
		// only the module's own records are scoped to it.
		runCtx := log.With(log.WithScope(ctx, "module"), "module", mod.Config.Name, "module-index", idx+1)

		err := runModule(runCtx, mod, sess, moduleArgs[idx]...)
		if err != nil {
			args.moduleErrCh <- err

			// Deliberate detail+summary logging (not log-and-return spam): this
			// agent-scope line records which module failed (name/index/total) —
			// metadata lost once waitModules flattens the error with %v and Run
			// logs the rpc-scope summary. The error is still returned via
			// moduleErrCh, not swallowed.
			mlog.Error("module failed", "err", err)
			cancel()

			return
		}
	}

	if ctx.Err() != nil {
		// A module that ends on cancellation, like a serial console after the
		// client quit, returns nil; that is a stop, not a success.
		l.Info("modules stopped: run canceled")
	} else {
		l.Info("all modules finished successfully")
	}

	cancel()
	close(args.moduleErrCh)
}

// waitModules is a state of the Run RPC.
//
// It waits for both module execution and broker workers to complete. Both
// channels use the error-only pattern:
//
//	Success: the channel closes without sending anything.
//	Failure: the channel sends an error, then closes.
//
// If multiple events (errors, closures, context cancellation) happen
// simultaneously, any of them may be processed first - this is acceptable.
//
// Errors: CodeCanceled/CodeDeadlineExceeded on context cancellation (via
// cancelCode); CodeAborted on a module failure; and for a broker error,
// CodeInvalidArgument for a client protocol violation (session.ErrBadFileTransfer),
// an already-typed connect code preserved, else CodeInternal (via brokerError).
func waitModules(ctx context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	brokerDone := false
	moduleDone := false

	for !brokerDone || !moduleDone {
		select {
		case <-ctx.Done():
			e := connect.NewError(cancelCode(ctx.Err()), fmt.Errorf("module execution aborted: %v", ctx.Err()))

			awaitModuleStop(ctx, args.moduleDone, args.stopGrace)

			return args, nil, e

		case brokerErr, ok := <-args.brokerErrCh:
			if !ok {
				// Broker channel closed = success
				brokerDone = true
			} else {
				// Broker only sends errors (never nil).
				return args, nil, brokerError(brokerErr)
			}

		case moduleErr, ok := <-args.moduleErrCh:
			if !ok {
				// Module channel closed = success
				moduleDone = true
			} else {
				// Module only sends errors (never nil).
				return args, nil, moduleError(moduleErr)
			}
		}
	}

	// Success: the broker's workers are awaited and the auto-lock is released by
	// the deferred cleanup in Run, which covers every exit path including a
	// panic, so no explicit teardown state is needed here.
	return args, nil, nil
}

// defaultModuleStopGrace bounds how long a canceled run waits for its module to
// return. A module that honours cancellation returns within its I/O timeout (the
// serial module's is 100ms); one that does not is left behind after this.
const defaultModuleStopGrace = 5 * time.Second

// awaitModuleStop blocks until the run's module goroutine has returned, or until
// grace (defaultModuleStopGrace if zero) elapses. Run releases the device once
// waitModules returns, so without this a client that quits and reconnects at once
// could find the serial port still open ("Serial port busy"). A nil done means
// no module was started.
func awaitModuleStop(ctx context.Context, done <-chan struct{}, grace time.Duration) {
	if done == nil {
		return
	}

	if grace == 0 {
		grace = defaultModuleStopGrace
	}

	select {
	case <-done:
	case <-time.After(grace):
		log.FromContext(ctx).Warn("module did not stop after the run was canceled", "grace", grace)
	}
}

// cancelCode maps a context cancellation error to its connect status code,
// defaulting to CodeCanceled. It is used at every site that converts a cancelled
// Run to a wire status, so cancellation maps to a single code across the RPC.
func cancelCode(err error) connect.Code {
	if errors.Is(err, context.DeadlineExceeded) {
		return connect.CodeDeadlineExceeded
	}

	return connect.CodeCanceled
}

// receiveError classifies an error from the initial stream Receive: a context
// cancellation maps via cancelCode, an already cancellation-coded connect error
// keeps its code, and anything else is CodeAborted (the run was aborted before it
// began).
func receiveError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return connect.NewError(cancelCode(err), err)
	}

	if code := connect.CodeOf(err); code == connect.CodeCanceled || code == connect.CodeDeadlineExceeded {
		return err
	}

	return connect.NewError(connect.CodeAborted, err)
}

// moduleError maps a terminal module error to the connect error that fails the
// Run: a context cancellation (e.g. a module that honors ctx and returns
// ctx.Err()) maps via cancelCode, keeping cancellation single-valued across the
// RPC; anything else is CodeAborted.
func moduleError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return connect.NewError(cancelCode(err), err)
	}

	return connect.NewError(connect.CodeAborted, fmt.Errorf("module failed: %v", err))
}

// brokerError maps a terminal broker error to the connect error that fails the
// Run: a client protocol violation (session.ErrBadFileTransfer) is
// CodeInvalidArgument, an already-typed connect error (e.g. CodeCanceled on
// client disconnect) keeps its code, and anything else is an internal fault.
func brokerError(err error) error {
	var connectErr *connect.Error

	switch {
	case errors.Is(err, session.ErrBadFileTransfer):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.As(err, &connectErr):
		return err
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
