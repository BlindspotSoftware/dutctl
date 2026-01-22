// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent"
	"github.com/BlindspotSoftware/dutctl/internal/fsm"
	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/pkg/module"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// runCmdArgs are arguments for the finite state machine in the Run RPC.
type runCmdArgs struct {
	// dependencies of the state machine
	stream     dutagent.Stream
	deviceList dut.Devlist
	locker     *dutagent.Locker
	user       string

	// fields for the states used during execution
	cmdMsg      *pb.Command
	dev         dut.Device
	cmd         dut.Command
	broker      *dutagent.Broker
	session     module.Session
	moduleErrCh chan error
	brokerErrCh <-chan error
}

// receiveCommandRPC is the first state of the Run RPC.
//
// It receives a message from the client. As the client could potentially send
// messages of various types, it filters for a command message and returns a error
// otherwise.
func receiveCommandRPC(_ context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	req, err := args.stream.Receive()
	if err != nil {
		e := connect.NewError(connect.CodeAborted, err)

		return args, nil, e
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
// It finds the device under test (DUT) based on the device name in the command message.
// and the command to run based on the command name in the command message.
// If the device is not found, or the command is not available at the respective device,
// it returns an error.
func findDUTCmd(_ context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	wantDev := args.cmdMsg.GetDevice()
	wantCmd := args.cmdMsg.GetCommand()

	dev, cmd, err := args.deviceList.FindCmd(wantDev, wantCmd)
	if err != nil {
		var code connect.Code
		if errors.Is(err, dut.ErrDeviceNotFound) || errors.Is(err, dut.ErrCommandNotFound) {
			code = connect.CodeInvalidArgument
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
func checkDeviceAccess(_ context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	err := args.locker.CheckAccess(args.cmdMsg.GetDevice(), args.user)
	if err != nil {
		if errors.Is(err, dutagent.ErrWrongOwner) {
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
// already holds an auto-lock from a previous race-lost step.
func acquireAutoLock(_ context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	_, err := args.locker.AutoLock(args.cmdMsg.GetDevice(), args.user)
	if err != nil {
		if errors.Is(err, dutagent.ErrWrongOwner) {
			return args, nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}

		return args, nil, connect.NewError(connect.CodeInternal, err)
	}

	return args, executeModules, nil
}

// releaseAutoLock is the final state of the Run RPC's happy path.
//
// It releases the command-scoped auto-lock acquired by acquireAutoLock. It
// never touches the explicit lock slot, so an explicit Lock the same owner
// holds for the device survives the run. ErrNotLocked is tolerated because
// a forced unlock by an admin may have wiped the slot concurrently.
func releaseAutoLock(ctx context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	err := args.locker.ClearAutoLock(args.cmdMsg.GetDevice(), args.user)
	if err != nil && !errors.Is(err, dutagent.ErrNotLocked) {
		log.FromContext(ctx).Warn("failed to release auto-lock", "device", args.cmdMsg.GetDevice(), "err", err)
	}

	return args, nil, nil
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
// It starts the execution the current command's modules. The execution is done
// in a separate goroutine, this state will not wait for the execution to finish.
// Further, worker goroutines will be started to serve the module-to-client communication
// during the module execution.
//
//nolint:funlen
func executeModules(ctx context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	// Module execution is the agent's core orchestration: scope it "agent" and
	// tag the device and command, which then descend to every record on this path.
	ctx = log.With(log.WithScope(ctx, "agent"), "device", args.cmdMsg.GetDevice(), "command", args.cmdMsg.GetCommand())
	l := log.FromContext(ctx)

	args.broker = &dutagent.Broker{}

	// Deferred initialization of the moduleErr channel: only create if not already provided
	// (tests may still pass a custom channel).
	if args.moduleErrCh == nil {
		args.moduleErrCh = make(chan error, 1)
	}

	rpcCtx := ctx
	modCtx, modCtxCancel := context.WithCancel(rpcCtx)

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
	go func() {
		defer modCtxCancel() // Ensure workers exit even if stream doesn't close

		cnt := len(args.cmd.Modules)

		for idx, mod := range args.cmd.Modules {
			if ctx.Err() != nil {
				l.Warn("execution aborted", "modules-done", idx, "modules-total", cnt, "err", ctx.Err())

				// Signal graceful shutdown and let any in-flight file transfers
				// finish before the workers exit (modCtx is cancelled by the defer).
				args.broker.Shutdown()
				args.broker.WaitForTransfersToComplete()

				return
			}

			// Announce the hand-off in the agent scope (this line is the
			// framework's, not the module's).
			mlog := l.With("module", mod.Config.Name, "module-index", idx+1, "modules-total", cnt)
			mlog.Info("running module")

			// Set the "module" scope on the context handed to the module, so
			// only the module's own records are scoped to it.
			runCtx := log.With(log.WithScope(rpcCtx, "module"), "module", mod.Config.Name, "module-index", idx+1)

			err := runModule(runCtx, mod, moduleSession, moduleArgs[idx]...)
			if err != nil {
				args.moduleErrCh <- err

				mlog.Error("module failed", "err", err)

				// Signal graceful shutdown and let any in-flight file transfers
				// finish before the workers exit (modCtx is cancelled by the defer).
				args.broker.Shutdown()
				args.broker.WaitForTransfersToComplete()

				return
			}
		}

		l.Info("all modules finished successfully")

		// Signal graceful shutdown and let any in-flight file transfers finish
		// before the workers exit (modCtx is cancelled by the defer).
		args.broker.Shutdown()
		args.broker.WaitForTransfersToComplete()

		close(args.moduleErrCh)
	}()

	return args, waitModules, nil
}

// waitModules is a state of the Run RPC.
//
// It waits for both module execution and broker workers to complete.
// Both channels use the error-only pattern:
// - Success: Channel closes without sending anything
// - Failure: Sends error, then closes
// If multiple events (errors, closures, context cancellation) happen simultaneously,
// any of them may be processed first - this is acceptable.
func waitModules(ctx context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	brokerDone := false
	moduleDone := false

	for !brokerDone || !moduleDone {
		select {
		case <-ctx.Done():
			e := connect.NewError(connect.CodeAborted, fmt.Errorf("module execution aborted: %v", ctx.Err()))

			return args, nil, e

		case brokerErr, ok := <-args.brokerErrCh:
			if !ok {
				// Broker channel closed = success
				brokerDone = true
			} else {
				// Broker only sends errors (never nil)
				e := connect.NewError(connect.CodeInternal, fmt.Errorf("broker error: %v", brokerErr))

				return args, nil, e
			}

		case moduleErr, ok := <-args.moduleErrCh:
			if !ok {
				// Module channel closed = success
				moduleDone = true
			} else {
				// Module only sends errors (never nil)
				e := connect.NewError(connect.CodeAborted, fmt.Errorf("module failed: %v", moduleErr))

				return args, nil, e
			}
		}
	}

	return args, releaseAutoLock, nil
}
