// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package main

import (
	"context"
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

	res := connect.NewResponse(&pb.CommandsResponse{
		Commands: a.devices.Cmds(device),
	})

	log.Print("Commands-RPC finished")

	return res, nil
}

func (a *rpcService) Details(
	_ context.Context,
	req *connect.Request[pb.DetailsRequest],
) (*connect.Response[pb.DetailsResponse], error) {
	log.Println("Server received Details request")

	wantDev := req.Msg.GetDevice()
	wantCmd := req.Msg.GetCmd()
	keyword := req.Msg.GetKeyword()

	_, cmd, err := findCmd(a.devices, wantDev, wantCmd)
	if err != nil {
		return nil, err
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

	if keyword != "help" {
		e := connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("unknown keyword %q, possible values are: 'help'", keyword),
		)

		return nil, e
	}

	res := connect.NewResponse(&pb.DetailsResponse{
		Details: helpStr,
	})

	log.Print("Details-RPC finished")

	return res, nil
}
