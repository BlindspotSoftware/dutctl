// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent"
	"github.com/BlindspotSoftware/dutctl/internal/fsm"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/pkg/module"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// runCmdArgs are arguments for the finite state machine in the Run RPC.
type runCmdArgs struct {
	// dependencies of the state machine
	stream     dutagent.Stream
	deviceList dut.Devlist

	// fields for the states used during execution
	cmdMsg      *pb.Command
	dev         dut.Device
	cmd         dut.Command
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

	return args, executeModules, nil
}

// executeModules is a state of the Run RPC.
//
// It starts the execution the current command's modules. The execution is done
// in a separate goroutine, this state will not wait for the execution to finish.
// Further, worker goroutines will be started to serve the module-to-client communication
// during the module execution.
func executeModules(ctx context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	broker := &dutagent.Broker{}

	// Deferred initialization of the moduleErr channel: only create if not already provided
	// (tests may still pass a custom channel).
	if args.moduleErrCh == nil {
		args.moduleErrCh = make(chan error, 1)
	}

	rpcCtx := ctx
	modCtx, modCtxCancel := context.WithCancel(rpcCtx)

	moduleSession, brokerErrCh := broker.Start(modCtx, args.stream)
	args.brokerErrCh = brokerErrCh
	args.session = moduleSession

	// Run the modules in a goroutine.
	// Termination of the module execution is signaled by closing the moduleErrCh channel.
	go func() {
		cnt := len(args.cmd.Modules)

		for idx, module := range args.cmd.Modules {
			if ctx.Err() != nil {
				log.Printf("Execution aborted, %d of %d modules done: %v", idx, cnt, ctx.Err())
				modCtxCancel()

				return
			}

			log.Printf("Running module %d of %d: %q", idx+1, cnt, module.Config.Name)

			var moduleArgs []string
			if module.Config.Main {
				moduleArgs = args.cmdMsg.GetArgs()
			} else {
				moduleArgs = module.Config.Args
			}

			err := module.Run(rpcCtx, moduleSession, moduleArgs...)
			if err != nil {
				args.moduleErrCh <- err

				log.Printf("Module %q failed: %v", module.Config.Name, err)
				modCtxCancel()

				return
			}
		}

		log.Print("All modules finished successfully")
		modCtxCancel()
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

	return args, nil, nil
}
