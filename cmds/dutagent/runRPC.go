// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent"
	"github.com/BlindspotSoftware/dutctl/internal/fsm"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// runCmdArgs arguments for the state machine in the Run RPC.
type runCmdArgs struct {
	// dependencies for the state machine

	stream     *connect.BidiStream[pb.RunRequest, pb.RunResponse]
	broker     *dutagent.Broker
	deviceList dut.Devlist

	// fields for the states used during execution

	cmdMsg    *pb.Command
	dev       dut.Device
	cmd       dut.Command
	moduleErr chan error
}

// Run is the handler for the Run RPC.
func (a *rpcService) Run(
	ctx context.Context,
	stream *connect.BidiStream[pb.RunRequest, pb.RunResponse],
) error {
	log.Println("Server received Run request")

	args := runCmdArgs{
		stream:     stream,
		broker:     &dutagent.Broker{},
		deviceList: a.devices,
		moduleErr:  make(chan error, 1),
	}

	var err error
	_, err = fsm.Run(ctx, args, receiveCommandRPC)

	var connectErr *connect.Error
	if err != nil && !errors.As(err, &connectErr) {
		// Wrap the error in a connect.Error if not done yet.
		err = connect.NewError(connect.CodeInternal, err)
	}

	log.Print("Run-RPC finished with error: ", err)

	return err
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

	dev, cmd, err := findCmd(args.deviceList, wantDev, wantCmd)
	if err != nil {
		return args, nil, err
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
	if args.broker == nil {
		e := connect.NewError(connect.CodeInternal, errors.New("broker is not initialized"))

		return args, nil, e
	}

	rpcCtx := ctx
	modCtx, modCtxCancel := context.WithCancel(rpcCtx)

	args.broker.Start(modCtx, args.stream)
	moduleSession := args.broker.ModuleSession()

	// Run the modules in a goroutine.
	// The termination of the module execution is signaled by sending a result to the args.moduleErr channel.
	go func() {
		cnt := len(args.cmd.Modules)

		for idx, module := range args.cmd.Modules {
			log.Printf("Running module %d of %d: %q", idx+1, cnt, module.Config.Name)

			var moduleArgs []string

			if module.Config.Main {
				moduleArgs = args.cmdMsg.GetArgs()
			} else if module.Config.Args != nil {
				moduleArgs = strings.Split(*module.Config.Args, " ")
			}

			err := module.Run(rpcCtx, moduleSession, moduleArgs...)
			if err != nil {
				args.moduleErr <- err
				log.Printf("Module %q failed: %v", module.Config.Name, err)
				modCtxCancel()

				return
			}

			if ctx.Err() != nil {
				log.Printf("Module execution aborted, %d of %d done: %v", idx+1, cnt, ctx.Err())
				modCtxCancel()

				return
			}
		}

		log.Print("All modules finished successfully")
		modCtxCancel()
		args.moduleErr <- nil // Signal that all modules finished successfully
	}()

	return args, waitModules, nil
}

// waitModules is a state of the Run RPC.
//
// It waits for the module execution to finish. The state will block until the module execution is finished.
func waitModules(ctx context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	log.Println("Waiting for module to finish")

	var (
		brokerDone bool
		moduleDone bool
	)

	for {
		select {
		case <-ctx.Done():
			e := connect.NewError(connect.CodeAborted, fmt.Errorf("module execution aborted: %v", ctx.Err()))
			log.Printf("Wait for Modules to finish: Context closed: %v", e)

			return args, nil, e
		case brokerErr := <-args.broker.Err():
			brokerDone = true

			if brokerErr != nil {
				// An error occurred with the communication to the module during the module execution.
				e := connect.NewError(connect.CodeInternal, fmt.Errorf("module environment error: %v", brokerErr))
				log.Printf("Wait for Modules to finish: Broker issue: %v", e)

				return args, nil, e
			}
		case moduleErr := <-args.moduleErr:
			moduleDone = true

			if moduleErr != nil {
				// The module execution failed.
				e := connect.NewError(connect.CodeAborted, fmt.Errorf("module execution failed: %v", moduleErr))
				log.Printf("Wait for Modules to finish: A module failed: %v", e)

				return args, nil, e
			}
		default:
			// If the modules are done, we also need to wait for the broker to finish any remaining communication.
			if brokerDone && moduleDone {
				log.Println("Module execution finished successfully")

				return args, nil, nil
			}
		}
	}
}
