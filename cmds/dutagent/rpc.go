// Copyright 2025 Blindspot Software
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
	"github.com/BlindspotSoftware/dutctl/internal/fsm"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// rpcService is the service implementation for the RPCs provided by dutagent.
type rpcService struct {
	devices dut.Devlist
}

// List is the handler for the List RPC.
func (a *rpcService) List(
	_ context.Context,
	_ *connect.Request[pb.ListRequest],
) (*connect.Response[pb.ListResponse], error) {
	log.Println("Server received List request")

	res := connect.NewResponse(&pb.ListResponse{
		Devices: a.devices.Names(),
	})

	log.Print("List-RPC finished")

	return res, nil
}

// Commands is the handler for the Commands RPC.
func (a *rpcService) Commands(
	_ context.Context,
	req *connect.Request[pb.CommandsRequest],
) (*connect.Response[pb.CommandsResponse], error) {
	log.Println("Server received Commands request")

	device := req.Msg.GetDevice()

	cmds, err := a.devices.CmdNames(device)
	if err != nil {
		var code connect.Code
		if errors.Is(err, dut.ErrDeviceNotFound) {
			code = connect.CodeInvalidArgument
		} else {
			code = connect.CodeInternal
		}

		e := connect.NewError(
			code,
			fmt.Errorf("device %q: %w", device, err),
		)

		return nil, e
	}

	res := connect.NewResponse(&pb.CommandsResponse{
		Commands: cmds,
	})

	log.Print("Commands-RPC finished")

	return res, nil
}

// Details is the handler for the Details RPC.
func (a *rpcService) Details(
	_ context.Context,
	req *connect.Request[pb.DetailsRequest],
) (*connect.Response[pb.DetailsResponse], error) {
	log.Println("Server received Details request")

	wantDev := req.Msg.GetDevice()
	wantCmd := req.Msg.GetCmd()
	keyword := req.Msg.GetKeyword()

	if keyword != "help" {
		e := connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("unknown keyword %q, possible values are: 'help'", keyword),
		)

		return nil, e
	}

	_, cmd, err := a.devices.FindCmd(wantDev, wantCmd)
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

		return nil, e
	}

	helpStr := buildCommandHelp(cmd)

	res := connect.NewResponse(&pb.DetailsResponse{
		Details: helpStr,
	})

	log.Print("Details-RPC finished")

	return res, nil
}

// buildCommandHelp constructs help text for a command based on its configuration.
func buildCommandHelp(cmd dut.Command) string {
	var helpStr string

	// Find help text: prefer interactive module's help, otherwise describe all modules
	for _, module := range cmd.Modules {
		if module.Config.Interactive {
			helpStr = module.Help()

			break
		}
	}

	// If no interactive module, provide overview of all modules
	if helpStr == "" {
		var moduleNames []string
		for _, module := range cmd.Modules {
			moduleNames = append(moduleNames, module.Config.Name)
		}

		helpStr = fmt.Sprintf("Command with %d module(s): %s",
			len(cmd.Modules), strings.Join(moduleNames, ", "))
	}

	// Append command args documentation if declared
	if len(cmd.Args) > 0 {
		helpStr += "\n\nArguments:\n"
		for _, arg := range cmd.Args {
			helpStr += fmt.Sprintf("  %s: %s\n", arg.Name, arg.Desc)
		}
	}

	return helpStr
}

// streamAdapter decouples a connect.BidiStream to the dutagent.Stream interface.
type streamAdapter struct {
	inner *connect.BidiStream[pb.RunRequest, pb.RunResponse]
}

func (a *streamAdapter) Send(msg *pb.RunResponse) error   { return a.inner.Send(msg) }
func (a *streamAdapter) Receive() (*pb.RunRequest, error) { return a.inner.Receive() }

// Run is the handler for the Run RPC.
func (a *rpcService) Run(
	ctx context.Context,
	stream *connect.BidiStream[pb.RunRequest, pb.RunResponse],
) error {
	log.Println("Server received Run request")

	fsmArgs := runCmdArgs{
		stream:     &streamAdapter{inner: stream},
		deviceList: a.devices,
	}

	_, err := fsm.Run(ctx, fsmArgs, receiveCommandRPC)

	var connectErr *connect.Error
	if err != nil && !errors.As(err, &connectErr) {
		// Wrap the error in a connect.Error if not done yet.
		err = connect.NewError(connect.CodeInternal, err)
	}

	if err != nil {
		log.Print("Run-RPC finished with error: ", err)
	} else {
		log.Print("Run-RPC finished successfully")
	}

	return err
}
