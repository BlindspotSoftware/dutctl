// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/locker"
	"github.com/BlindspotSoftware/dutctl/internal/fsm"
	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/pkg/lock"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// rpcService is the service implementation for the RPCs provided by dutagent.
type rpcService struct {
	devices dut.Devlist
	locker  *locker.Locker
}

// userFromHeader returns the calling user's identity from a request header,
// or a unique anonymous placeholder when the header is missing.
func userFromHeader(h http.Header) string {
	if user := h.Get(lock.UserHeader); user != "" {
		return user
	}

	return lock.AnonymousUser()
}

// rpcLogger returns a logger scoped to the RPC subsystem and tagged with the
// handler's method name, derived from the logger carried in ctx.
func rpcLogger(ctx context.Context, method string) *slog.Logger {
	return log.Scope(log.FromContext(ctx), "rpc").With("rpc", method)
}

// List is the handler for the List RPC.
func (a *rpcService) List(
	ctx context.Context,
	_ *connect.Request[pb.ListRequest],
) (*connect.Response[pb.ListResponse], error) {
	l := rpcLogger(ctx, "List")
	l.Info("request received")

	locks := a.locker.StatusAll()

	names := a.devices.Names()
	infos := make([]*pb.DeviceInfo, 0, len(names))

	for _, name := range names {
		info := &pb.DeviceInfo{Name: name}

		if explicit := locks[name].Explicit; explicit != nil {
			info.Lock = &pb.LockInfo{
				Owner:     explicit.Owner,
				LockedAt:  explicit.LockedAt.Unix(),
				ExpiresAt: explicit.ExpiresAt.Unix(),
			}
		}

		infos = append(infos, info)
	}

	res := connect.NewResponse(&pb.ListResponse{
		Devices: infos,
	})

	l.Info("request finished")

	return res, nil
}

// Commands is the handler for the Commands RPC.
func (a *rpcService) Commands(
	ctx context.Context,
	req *connect.Request[pb.CommandsRequest],
) (*connect.Response[pb.CommandsResponse], error) {
	l := rpcLogger(ctx, "Commands")
	l.Info("request received")

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

	l.Info("request finished")

	return res, nil
}

// Details is the handler for the Details RPC.
func (a *rpcService) Details(
	ctx context.Context,
	req *connect.Request[pb.DetailsRequest],
) (*connect.Response[pb.DetailsResponse], error) {
	l := rpcLogger(ctx, "Details")
	l.Info("request received")

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

	l.Info("request finished")

	return res, nil
}

// Lock is the handler for the Lock RPC.
func (a *rpcService) Lock(
	ctx context.Context,
	req *connect.Request[pb.LockRequest],
) (*connect.Response[pb.LockResponse], error) {
	l := rpcLogger(ctx, "Lock")
	l.Info("request received")

	device := req.Msg.GetDevice()
	user := userFromHeader(req.Header())

	_, err := a.devices.Find(device)
	if err != nil {
		code := connect.CodeInternal
		if errors.Is(err, dut.ErrDeviceNotFound) {
			code = connect.CodeInvalidArgument
		}

		return nil, connect.NewError(code, fmt.Errorf("device %q: %w", device, err))
	}

	dur := time.Duration(req.Msg.GetDurationSeconds()) * time.Second

	info, lockErr := a.locker.Lock(device, user, dur)
	if lockErr != nil {
		switch {
		case errors.Is(lockErr, locker.ErrWrongOwner):
			return nil, connect.NewError(connect.CodeFailedPrecondition, lockErr)
		case errors.Is(lockErr, locker.ErrInvalidDuration):
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

	l.Info("lock acquired", "device", device, "owner", info.Owner)

	return res, nil
}

// Unlock is the handler for the Unlock RPC.
func (a *rpcService) Unlock(
	ctx context.Context,
	req *connect.Request[pb.UnlockRequest],
) (*connect.Response[pb.UnlockResponse], error) {
	l := rpcLogger(ctx, "Unlock")
	l.Info("request received")

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
		case errors.Is(err, locker.ErrWrongOwner):
			return nil, connect.NewError(connect.CodePermissionDenied, err)
		case errors.Is(err, locker.ErrNotLocked):
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		default:
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	l.Info("lock released", "device", device, "user", user, "forced", req.Msg.GetForce())

	return connect.NewResponse(&pb.UnlockResponse{}), nil
}

// streamAdapter decouples a connect.BidiStream to the session.Stream interface.
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
	user := userFromHeader(stream.RequestHeader())

	// Set the RPC scope once; it flows through the FSM, the session backend and
	// the modules on ctx, so each only logs its own concern.
	ctx = log.With(log.WithScope(ctx, "rpc"), "rpc", "Run", "user", user)
	l := log.FromContext(ctx)
	l.Info("request received")

	fsmArgs := runCmdArgs{
		stream:     &streamAdapter{inner: stream},
		deviceList: a.devices,
		locker:     a.locker,
		user:       user,
	}

	finalArgs, err := fsm.Run(ctx, fsmArgs, receiveCommandRPC)

	// Safety net for error paths that short-circuit the FSM before
	// releaseAutoLock runs. Delegating to the state function keeps the
	// cleanup logic in one place. The state tolerates ErrNotLocked, so a
	// happy-path call (where the FSM already released the auto-lock) is a
	// harmless no-op.
	if finalArgs.cmdMsg != nil {
		releaseAutoLock(ctx, finalArgs) //nolint:errcheck // state never returns an error
	}

	var connectErr *connect.Error
	if err != nil && !errors.As(err, &connectErr) {
		// Wrap the error in a connect.Error if not done yet.
		err = connect.NewError(connect.CodeInternal, err)
	}

	if err != nil {
		l.Error("request finished with error", "err", err)
	} else {
		l.Info("request finished successfully")
	}

	return err
}
