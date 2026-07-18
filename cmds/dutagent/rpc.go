// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/auth"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/locker"
	"github.com/BlindspotSoftware/dutctl/internal/fsm"
	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/internal/rpc"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// rpcService is the service implementation for the RPCs provided by dutagent.
type rpcService struct {
	devices dut.Devlist
	locker  *locker.Locker
}

// rpcLogger returns a logger scoped to the RPC subsystem and tagged with the
// handler's method name, derived from the logger carried in ctx.
func rpcLogger(ctx context.Context, method string) *slog.Logger {
	return log.Scope(log.FromContext(ctx), "rpc").With("rpc", method)
}

// caller returns the identity of the RPC caller, attached to ctx by the
// identity interceptor. A missing identity means the interceptor was not
// installed on the service, which is a server bug, so it maps to CodeInternal.
func caller(ctx context.Context) (auth.Identity, error) {
	id, ok := auth.FromContext(ctx)
	if !ok {
		return auth.Identity{}, connect.NewError(connect.CodeInternal, errors.New("no caller identity in request context"))
	}

	return id, nil
}

// requireNamed rejects an anonymous (header-less) caller with CodeUnauthenticated.
// Lock and a normal Unlock require a stable identity: an anonymous caller is
// minted a fresh identity per request, so it could never release a lock it took.
// Run and a forced Unlock do not call this — Run's auto-lock lives within one
// request, and force-release is the cooperative escape hatch open to anyone.
func requireNamed(id auth.Identity) error {
	if id.IsAnonymous() {
		return connect.NewError(connect.CodeUnauthenticated,
			errors.New("operation requires an identified caller; set the From header"))
	}

	return nil
}

// expiresAtUnix renders a lock's expiry as Unix seconds, mapping the zero time —
// a lock with no time-based expiry, such as an auto-lock — to 0 rather than a
// spurious year-1 timestamp. This matches the proto contract, where 0 on an
// expires_at field means no expiry.
func expiresAtUnix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.Unix()
}

// List is the handler for the List RPC. It never returns an error.
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

		// Report whichever slot holds the device so a busy device never reads as
		// free. Explicit shadows auto: when both are held (necessarily by the same
		// owner) the explicit lock carries the meaningful expiry, while an auto
		// lock surfaces with a zero expiry, which the client renders as "in use".
		state := locks[name]

		held := state.Explicit
		if held == nil {
			held = state.Auto
		}

		if held != nil {
			info.Lock = &pb.LockInfo{
				Owner:     held.Owner,
				LockedAt:  held.LockedAt.Unix(),
				ExpiresAt: expiresAtUnix(held.ExpiresAt),
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
//
// Errors: CodeNotFound for an unknown device (dut.ErrDeviceNotFound);
// CodeInternal otherwise.
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
			code = connect.CodeNotFound
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
//
// Errors: CodeInvalidArgument for an unknown keyword; CodeNotFound for an unknown
// device or command (dut.ErrDeviceNotFound / dut.ErrCommandNotFound); CodeInternal
// otherwise.
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
			code = connect.CodeNotFound
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

// defaultLockDuration is applied when a Lock request carries no duration
// (duration_seconds == 0). Clients that omit the duration defer this policy to
// the agent.
const defaultLockDuration = 30 * time.Minute

// Lock is the handler for the Lock RPC.
//
// A zero duration means "unset": the agent substitutes defaultLockDuration. A
// negative duration is rejected. An anonymous caller is rejected: a lock must be
// releasable by its taker, which an anonymous, per-request identity cannot be.
//
// Errors: CodeUnauthenticated for an anonymous caller; CodeNotFound for an unknown
// device (dut.ErrDeviceNotFound); CodeInvalidArgument for a negative duration
// (locker.ErrInvalidDuration); CodeFailedPrecondition when another owner holds the
// device (locker.ErrWrongOwner); CodeInternal otherwise.
func (a *rpcService) Lock(
	ctx context.Context,
	req *connect.Request[pb.LockRequest],
) (*connect.Response[pb.LockResponse], error) {
	l := rpcLogger(ctx, "Lock")
	l.Info("request received")

	device := req.Msg.GetDevice()

	identity, err := caller(ctx)
	if err != nil {
		return nil, err
	}

	err = requireNamed(identity)
	if err != nil {
		return nil, err
	}

	user := identity.User()

	_, err = a.devices.Find(device)
	if err != nil {
		code := connect.CodeInternal
		if errors.Is(err, dut.ErrDeviceNotFound) {
			code = connect.CodeNotFound
		}

		return nil, connect.NewError(code, fmt.Errorf("device %q: %w", device, err))
	}

	dur := time.Duration(req.Msg.GetDurationSeconds()) * time.Second
	if dur == 0 {
		dur = defaultLockDuration
	}

	info, lockErr := a.locker.Lock(device, user, dur)
	if lockErr != nil {
		switch {
		// ErrWrongOwner is CodeFailedPrecondition on acquire (the device is busy) —
		// deliberately different from release in Unlock, which is CodePermissionDenied
		// (you may not unlock another user's lock).
		case errors.Is(lockErr, locker.ErrWrongOwner):
			return nil, connect.NewError(connect.CodeFailedPrecondition, lockErr)
		case errors.Is(lockErr, locker.ErrInvalidDuration):
			return nil, connect.NewError(connect.CodeInvalidArgument, lockErr)
		default:
			return nil, connect.NewError(connect.CodeInternal, lockErr)
		}
	}

	res := connect.NewResponse(&pb.LockResponse{
		Device:    device,
		Owner:     info.Owner,
		LockedAt:  info.LockedAt.Unix(),
		ExpiresAt: expiresAtUnix(info.ExpiresAt),
	})

	l.Info("lock acquired", "device", device, "owner", info.Owner)

	return res, nil
}

// Unlock is the handler for the Unlock RPC. A normal release requires a named
// caller; a forced release (the cooperative override) does not.
//
// Errors: CodeUnauthenticated for an anonymous non-force release;
// CodePermissionDenied when another owner holds the lock (locker.ErrWrongOwner);
// CodeFailedPrecondition when the device is not locked (locker.ErrNotLocked);
// CodeInternal otherwise.
func (a *rpcService) Unlock(
	ctx context.Context,
	req *connect.Request[pb.UnlockRequest],
) (*connect.Response[pb.UnlockResponse], error) {
	l := rpcLogger(ctx, "Unlock")
	l.Info("request received")

	device := req.Msg.GetDevice()

	identity, err := caller(ctx)
	if err != nil {
		return nil, err
	}

	user := identity.User()

	if req.Msg.GetForce() {
		err = a.locker.ForceClearLock(device)
	} else {
		err = requireNamed(identity)
		if err != nil {
			return nil, err
		}

		err = a.locker.ClearLock(device, user)
	}

	if err != nil {
		switch {
		// ErrWrongOwner is CodePermissionDenied on release (you may not unlock
		// another user's lock) — deliberately different from acquire in Lock/Run,
		// where a device held by someone else is CodeFailedPrecondition (busy).
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

// clearAutoLock releases the command-scoped auto-lock for device held by user.
// It never touches the explicit lock slot, so an explicit Lock the same owner
// holds for the device survives the run. ErrNotLocked is tolerated because a
// forced unlock by an admin may have wiped the slot concurrently; any other
// failure is logged rather than returned, as this runs during Run teardown
// (including panic unwinding), where no caller is left to handle it.
func clearAutoLock(ctx context.Context, lk *locker.Locker, device, user string) {
	err := lk.ClearAutoLock(device, user)
	if err != nil && !errors.Is(err, locker.ErrNotLocked) {
		log.FromContext(ctx).Warn("failed to release auto-lock", "device", device, "err", err)
	}
}

// Run is the handler for the Run RPC.
//
// It drives the finite state machine (see states.go); each state maps its failure
// to a connect.Code. Run passes an already-typed *connect.Error through unchanged,
// maps a raw context cancellation to CodeCanceled/CodeDeadlineExceeded (via
// cancelCode), and wraps anything else as CodeInternal, so every failure reaches
// the client with a code.
func (a *rpcService) Run(
	ctx context.Context,
	stream *connect.BidiStream[pb.RunRequest, pb.RunResponse],
) error {
	identity, err := caller(ctx)
	if err != nil {
		return err
	}

	user := identity.User()

	// Set the RPC scope once; it flows through the FSM, the session backend and
	// the modules on ctx, so each only logs its own concern.
	ctx = log.With(log.WithScope(ctx, "rpc"), "rpc", "Run", "user", user)
	l := log.FromContext(ctx)
	l.Info("request received")

	autoLock := &autoLockHold{}

	// Release the command-scoped auto-lock on every exit path. Deferred so it
	// runs even while a panic in a state function unwinds past the FSM (fsm.Run
	// does not recover), which would otherwise leave the device auto-locked with
	// no expiry until an agent restart. It fires only once the lock was acquired
	// (acquireAutoLock sets held); ClearAutoLock tolerates a concurrent forced
	// unlock.
	defer func() {
		if autoLock.held {
			clearAutoLock(ctx, a.locker, autoLock.device, user)
		}
	}()

	fsmArgs := runCmdArgs{
		stream:     rpc.NewRunStream(stream),
		deviceList: a.devices,
		locker:     a.locker,
		user:       user,
		autoLock:   autoLock,
	}

	_, err = fsm.Run(ctx, fsmArgs, receiveCommandRPC)

	var connectErr *connect.Error
	if err != nil && !errors.As(err, &connectErr) {
		// Wrap the error in a connect.Error if not done yet. A raw context error
		// from the FSM boundary becomes the matching cancellation code (kept in
		// sync with waitModules via cancelCode); anything else is internal.
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			err = connect.NewError(cancelCode(err), err)
		default:
			err = connect.NewError(connect.CodeInternal, err)
		}
	}

	if err != nil {
		l.Error("request finished with error", "err", err)
	} else {
		l.Info("request finished successfully")
	}

	return err
}
