// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent"
	"github.com/BlindspotSoftware/dutctl/internal/fsm"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/pkg/lock"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// rpcService is the service implementation for the RPCs provided by dutagent.
type rpcService struct {
	devices dut.Devlist
	locker  *dutagent.Locker
}

// userFromHeader returns the calling user's identity from a request header,
// or a unique anonymous placeholder when the header is missing.
func userFromHeader(h http.Header) string {
	if user := h.Get(lock.UserHeader); user != "" {
		return user
	}

	return lock.AnonymousUser()
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

	helpStr := cmd.HelpText()

	res := connect.NewResponse(&pb.DetailsResponse{
		Details: helpStr,
	})

	log.Print("Details-RPC finished")

	return res, nil
}

// Lock is the handler for the Lock RPC.
func (a *rpcService) Lock(
	_ context.Context,
	req *connect.Request[pb.LockRequest],
) (*connect.Response[pb.LockResponse], error) {
	log.Println("Server received Lock request")

	device := req.Msg.GetDevice()
	user := userFromHeader(req.Header())

	if _, ok := a.devices[device]; !ok {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("device %q: %w", device, dut.ErrDeviceNotFound),
		)
	}

	dur := time.Duration(req.Msg.GetDurationSeconds()) * time.Second

	info, lockErr := a.locker.Lock(device, user, dur)
	if lockErr != nil {
		switch {
		case errors.Is(lockErr, dutagent.ErrWrongOwner):
			return nil, connect.NewError(connect.CodeFailedPrecondition, lockErr)
		case errors.Is(lockErr, dutagent.ErrInvalidDuration):
			return nil, connect.NewError(connect.CodeInvalidArgument, lockErr)
		default:
			return nil, connect.NewError(connect.CodeInternal, lockErr)
		}
	}

	var expiresAt int64
	if !info.ExpiresAt.IsZero() {
		expiresAt = info.ExpiresAt.Unix()
	}

	res := connect.NewResponse(&pb.LockResponse{
		Device:    device,
		Owner:     info.Owner,
		LockedAt:  info.LockedAt.Unix(),
		ExpiresAt: expiresAt,
	})

	log.Print("Lock-RPC finished")

	return res, nil
}

// Unlock is the handler for the Unlock RPC.
func (a *rpcService) Unlock(
	_ context.Context,
	req *connect.Request[pb.UnlockRequest],
) (*connect.Response[pb.UnlockResponse], error) {
	log.Println("Server received Unlock request")

	device := req.Msg.GetDevice()
	user := userFromHeader(req.Header())

	var err error
	if req.Msg.GetForce() {
		err = a.locker.ForceClearLock(device)
	} else {
		err = a.locker.ClearLock(device, user)
	}

	if err != nil {
		switch {
		case errors.Is(err, dutagent.ErrWrongOwner):
			return nil, connect.NewError(connect.CodePermissionDenied, err)
		case errors.Is(err, dutagent.ErrNotLocked):
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		default:
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	log.Print("Unlock-RPC finished")

	return connect.NewResponse(&pb.UnlockResponse{}), nil
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
