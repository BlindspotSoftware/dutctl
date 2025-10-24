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

//nolint:funlen
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

	var (
		helpStr   string
		foundMain bool
	)

	for _, module := range cmd.Modules {
		if module.Config.Main {
			foundMain = true
			helpStr = module.Help()
		}
	}

	if !foundMain {
		e := connect.NewError(
			connect.CodeInternal,
			fmt.Errorf("no main module found for command %q at device %q", wantCmd, wantDev),
		)

		return nil, e
	}

	res := connect.NewResponse(&pb.DetailsResponse{
		Details: helpStr,
	})

	log.Print("Details-RPC finished")

	return res, nil
}
