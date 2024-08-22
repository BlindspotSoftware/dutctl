package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/fsm"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// runCmdArgs arguments for the state machine in the Run RPC.
type runCmdArgs struct {
	// dependencies for the state machine

	stream     *connect.BidiStream[pb.RunRequest, pb.RunResponse]
	sesh       *session
	workerWG   *sync.WaitGroup // TODO: eventually replace with another done channel or take a look at sync.ErrGroup
	deviceList devlist

	// fields for the states used during execution

	cmdMsg *pb.Command
	dev    dut.Device
	cmd    dut.Command
}

// Run is the handler for the Run RPC.
func (a *dutagent) Run(
	ctx context.Context,
	stream *connect.BidiStream[pb.RunRequest, pb.RunResponse],
) error {
	log.Println("Server received Run request")

	args := runCmdArgs{
		stream: stream,
		sesh: &session{
			//nolint:forbidigo
			print:   make(chan string), // this should not trigger the linter
			stdin:   make(chan []byte),
			stdout:  make(chan []byte),
			stderr:  make(chan []byte),
			fileReq: make(chan string),
			file:    make(chan chan []byte),
			done:    make(chan struct{}),
		},
		workerWG:   &sync.WaitGroup{},
		deviceList: a.devices,
	}

	_, err := fsm.Run(ctx, args, receiveCommandRPC)

	// TODO: change error handling to create a connect.Error here and wrap the
	// returned error in it. Also, may use the returned args to provide more
	// context in the error message.

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
	wantCmd := args.cmdMsg.GetCmd()

	device, ok := args.deviceList[wantDev]
	if !ok {
		e := connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("device %q does not exist", wantDev))

		return args, nil, e
	}

	cmd, ok := device.Cmds[wantCmd]
	if !ok {
		e := connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("device %q does not have command %q", wantDev, wantCmd))

		return args, nil, e
	}

	if len(cmd.Modules) == 0 {
		e := connect.NewError(connect.CodeInternal, fmt.Errorf("no modules set for command %q", wantCmd))

		return args, nil, e
	}

	if len(cmd.Modules) > 1 {
		e := connect.NewError(connect.CodeUnimplemented, errors.New("multiple modules per command are not supported yet"))

		return args, nil, e
	}

	args.dev = device
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
	if args.sesh == nil {
		e := connect.NewError(connect.CodeInternal, errors.New("module session is not initialized"))

		return args, nil, e
	}

	// Run the module in a goroutine.
	// The termination of the module execution is signaled by closing the done channel.
	go func() {
		args.sesh.err = args.cmd.Modules[0].Run(ctx, args.sesh, args.cmdMsg.GetArgs()...)
		log.Println("Module finished and returned error: ", args.sesh.err)
		close(args.sesh.done)
	}()

	// TODO: Maybe change

	// Start a worker for sending messages that are collected by the session form the module to the client.
	// This worker will be synchronized with the module execution by sessions done channel. The worker will
	// return when the module execution is finished.
	// Use a WaitGroup for synchronization as there might be messages left to send to the client after the
	// module finished.
	args.workerWG.Add(1)

	go func() {
		defer args.workerWG.Done()
		log.Println("Starting send-to-client worker")
		//nolint:errcheck
		toClientWorker(args.stream, args.sesh) // TODO: error handling
		log.Println("The send-to-client worker returned")
	}()

	// Start a worker for receiving messages from the client and pass them to the module.
	// Do not use a WaitGroup here, as the worker blocks on receive calls to the client.
	// In case of a non-interactive module (client does not send further messages), the worker will block forever.
	// and waiting for it will never return.
	// However, if the stream is closed, the receive calls to the client unblock and the worker will return.
	go func() {
		log.Println("Starting receive-from-client worker")
		//nolint:errcheck
		fromClientWorker(args.stream, args.sesh) // TODO: error handling
		log.Println("The receive-from-client worker returned")
	}()

	return args, waitModules, nil
}

// waitModules is a state of the Run RPC.
//
// It waits for the module execution to finish. The state will block until the module execution is finished.
func waitModules(_ context.Context, args runCmdArgs) (runCmdArgs, fsm.State[runCmdArgs], error) {
	log.Println("Waiting for session to finish")

	args.workerWG.Wait()

	log.Println("Session finished")

	return args, nil, args.sesh.err
}
