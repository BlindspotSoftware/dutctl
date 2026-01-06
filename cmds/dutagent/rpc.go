// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
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
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("unknown keyword %q, possible values are: 'help'", keyword),
		)
	}

	_, cmd, err := a.devices.FindCmd(wantDev, wantCmd)
	if err != nil {
		code := connect.CodeInternal
		if errors.Is(err, dut.ErrDeviceNotFound) || errors.Is(err, dut.ErrCommandNotFound) {
			code = connect.CodeInvalidArgument
		}

		return nil, connect.NewError(code, fmt.Errorf("device %q, command %q: %w", wantDev, wantCmd, err))
	}

	helpText := buildCommandHelp(cmd)

	log.Print("Details-RPC finished")

	return connect.NewResponse(&pb.DetailsResponse{Details: helpText}), nil
}

// buildCommandHelp constructs comprehensive help text for a command.
func buildCommandHelp(cmd dut.Command) string {
	var helpStr strings.Builder

	if cmd.Desc != "" {
		helpStr.WriteString("Description:\n  ")
		helpStr.WriteString(cmd.Desc)
		helpStr.WriteString("\n\n")
	}

	if len(cmd.Args) > 0 {
		helpStr.WriteString("Arguments:\n")

		argNames := make([]string, 0, len(cmd.Args))
		for name := range cmd.Args {
			argNames = append(argNames, name)
		}

		slices.Sort(argNames)

		for i, name := range argNames {
			helpStr.WriteString(fmt.Sprintf("  %d. %s: %s\n", i+1, name, cmd.Args[name]))
		}

		helpStr.WriteString("\n")
	}

	if len(cmd.Modules) > 0 {
		helpStr.WriteString("Modules:\n")

		for i, module := range cmd.Modules {
			helpStr.WriteString(fmt.Sprintf("\n%d. %s\n", i+1, module.Config.Name))

			if moduleHelp := module.Help(); moduleHelp != "" {
				for _, line := range strings.Split(moduleHelp, "\n") {
					if line != "" {
						helpStr.WriteString("   ")
						helpStr.WriteString(line)
						helpStr.WriteString("\n")
					}
				}
			}
		}
	}

	return helpStr.String()
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
