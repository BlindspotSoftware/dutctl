// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent"
	"github.com/BlindspotSoftware/dutctl/internal/fsm"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// rpcService is the service implementation for the RPCs provided by dutagent.
type rpcService struct {
	devices dut.Devlist
	locker  *dutagent.Locker
}

// List is the handler for the List RPC.
func (a *rpcService) List(
	_ context.Context,
	_ *connect.Request[pb.ListRequest],
) (*connect.Response[pb.ListResponse], error) {
	log.Println("Server received List request")

	names := a.devices.Names()
	lockInfos := lockInfoProto(names, a.locker.StatusAll())

	res := connect.NewResponse(&pb.ListResponse{
		Devices:  names,
		LockInfo: lockInfos,
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

// lockInfoProto converts a StatusAll map into a []*pb.DeviceLockInfo slice ordered
// by devices (same order as the devices slice in ListResponse).
func lockInfoProto(devices []string, locks map[string]dutagent.LockInfo) []*pb.DeviceLockInfo {
	result := make([]*pb.DeviceLockInfo, len(devices))

	for idx, dev := range devices {
		info, locked := locks[dev]
		entry := &pb.DeviceLockInfo{Device: dev, Locked: locked}

		if locked {
			entry.Owner = info.Owner
			entry.LockedAt = info.LockedAt.Unix()
			entry.ExpiresAt = info.ExpiresAt.Unix()
		}

		result[idx] = entry
	}

	return result
}

// Lock is the handler for the Lock RPC.
func (a *rpcService) Lock(
	_ context.Context,
	req *connect.Request[pb.LockRequest],
) (*connect.Response[pb.LockResponse], error) {
	log.Println("Server received Lock request")

	device := req.Msg.GetDevice()
	owner := req.Msg.GetOwner()
	durationSeconds := req.Msg.GetDurationSeconds()

	if device == "" && owner == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("device and owner must not both be empty"))
	}

	if durationSeconds <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("duration_seconds must be positive"))
	}

	duration := time.Duration(durationSeconds) * time.Second

	if _, ok := a.devices[device]; !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("device %q: %w", device, dut.ErrDeviceNotFound))
	}

	info, err := a.locker.Lock(device, owner, duration)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	res := connect.NewResponse(&pb.LockResponse{
		Device:    device,
		Owner:     info.Owner,
		LockedAt:  info.LockedAt.Unix(),
		ExpiresAt: info.ExpiresAt.Unix(),
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
	owner := req.Msg.GetOwner()

	if _, ok := a.devices[device]; !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("device %q: %w", device, dut.ErrDeviceNotFound))
	}

	err := a.locker.Unlock(device, owner)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	log.Print("Unlock-RPC finished")

	return connect.NewResponse(&pb.UnlockResponse{}), nil
}

// LockStatus is the handler for the LockStatus RPC.
func (a *rpcService) LockStatus(
	_ context.Context,
	req *connect.Request[pb.LockStatusRequest],
) (*connect.Response[pb.LockStatusResponse], error) {
	log.Println("Server received LockStatus request")

	device := req.Msg.GetDevice()

	var infos []*pb.DeviceLockInfo

	if device == "" {
		names := a.devices.Names()
		locks := a.locker.StatusAll()
		infos = lockInfoProto(names, locks)
	} else {
		if _, ok := a.devices[device]; !ok {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("device %q: %w", device, dut.ErrDeviceNotFound))
		}

		entry := &pb.DeviceLockInfo{Device: device}

		if info, locked := a.locker.Status(device); locked {
			entry.Locked = true
			entry.Owner = info.Owner
			entry.LockedAt = info.LockedAt.Unix()
			entry.ExpiresAt = info.ExpiresAt.Unix()
		}

		infos = []*pb.DeviceLockInfo{entry}
	}

	log.Print("LockStatus-RPC finished")

	return connect.NewResponse(&pb.LockStatusResponse{Locks: infos}), nil
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
		locker:     a.locker,
	}

	finalArgs, err := fsm.Run(ctx, fsmArgs, receiveCommandRPC)

	// Release any auto-acquired lock regardless of execution outcome.
	if finalArgs.autoLocked && finalArgs.cmdMsg != nil {
		device := finalArgs.cmdMsg.GetDevice()
		owner := finalArgs.cmdMsg.GetOwner()

		unlockErr := a.locker.Unlock(device, owner)
		if unlockErr != nil {
			log.Printf("Run: failed to release auto-lock on %q: %v", device, unlockErr)
		}
	}

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
