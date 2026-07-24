// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"sync"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/buildinfo"
	"github.com/BlindspotSoftware/dutctl/internal/compat"
	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/internal/rpc"
	"github.com/BlindspotSoftware/dutctl/pkg/headers"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

type agent struct {
	sync.Mutex

	// address is the address of the DUT agent.
	address string
	// client is the Connect RPC client for the DUT agent.
	// Do not use this client directly, but use agent's conn method.
	client dutctlv1connect.DeviceServiceClient
}

// conn returns the RPC client that connects to the DUT agent.
func (a *agent) conn(ctx context.Context) dutctlv1connect.DeviceServiceClient {
	a.Lock()
	defer a.Unlock()

	if a.client == nil {
		// ctx is used only for this log line. Do NOT bind the client's dial or
		// lifetime to it: the client is cached and shared across requests, so
		// tying it to a single request's context would be a bug. A per-RPC
		// deadline belongs on the call context passed to the forwarders.
		log.FromContext(ctx).Debug("spawning client for agent", "agent", a.address)
		a.client = rpc.NewDeviceClient(a.address)
	}

	return a.client
}

// rpcService is the service implementation for the RPCs provided by dutserver.
// It implements both the DeviceService that clients would otherwise call on a
// dutagent directly and the RelayService that agents use to register with the
// server.
type rpcService struct {
	// UnimplementedDeviceServiceHandler provides default CodeUnimplemented
	// responses for DeviceService RPCs that dutserver does not forward,
	// such as Lock and Unlock.
	dutctlv1connect.UnimplementedDeviceServiceHandler

	mu sync.RWMutex

	// agents holds handles of the registered DUT agents.
	agents map[string]*agent
}

// findAgent returns the handle for the DUT agent, that controls the device with the given name.
func (s *rpcService) findAgent(device string) (*agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if agent, ok := s.agents[device]; ok {
		return agent, nil
	}

	return nil, fmt.Errorf("device %q not found", device)
}

// addAgent tries to register devices handled by an agent with address.
// If one of the provided devices already exists an error is returned and none of the devices will be stored.
func (s *rpcService) addAgent(ctx context.Context, address string, devices []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	l := log.FromContext(ctx)

	for _, device := range devices {
		if _, exists := s.agents[device]; exists {
			l.Warn("rejecting registration: device already registered", "device", device, "agent", address)

			return fmt.Errorf("device %q already registered", device)
		}
	}

	for _, device := range devices {
		s.agents[device] = &agent{address: address}
	}

	l.Info("agent registered", "agent", address, "devices", devices)

	return nil
}

// List returns the names of all devices registered with dutserver. Unlike the
// other handlers it aggregates the local registry rather than forwarding to an
// agent, and reports no lock state because dutserver does not track locks.
func (s *rpcService) List(
	ctx context.Context,
	_ *connect.Request[pb.ListRequest],
) (*connect.Response[pb.ListResponse], error) {
	l := log.FromContext(log.With(log.WithScope(ctx, "rpc"), "rpc", "List"))
	l.Info("request received")

	names := slices.Sorted(maps.Keys(s.agents))
	infos := make([]*pb.DeviceInfo, 0, len(names))

	// dutserver does not track lock state; Lock is left unset.
	for _, name := range names {
		infos = append(infos, &pb.DeviceInfo{Name: name})
	}

	res := connect.NewResponse(&pb.ListResponse{
		Devices: infos,
	})

	l.Info("request finished")

	return res, nil
}

// Commands forwards a Commands request to the agent that controls the requested
// device and returns the agent's response.
//
//nolint:dupl // Commands and Details are parallel unary forwarders; dedup is tracked by the generic-forwarder TODO.
func (s *rpcService) Commands(
	ctx context.Context,
	req *connect.Request[pb.CommandsRequest],
) (*connect.Response[pb.CommandsResponse], error) {
	device := req.Msg.GetDevice()
	ctx = log.With(log.WithScope(ctx, "rpc"), "rpc", "Commands", "device", device)
	l := log.FromContext(ctx)
	l.Info("request received")

	agent, err := s.findAgent(device)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	res, err := forwardCommandsReq(log.WithScope(ctx, "relay"), agent.address, req)
	if err != nil {
		l.Error("forwarding to agent failed", "agent", agent.address, "err", err)

		// Preserve the downstream agent's status code when the failure is already a
		// connect error, instead of flattening every failure to CodeInternal.
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, err
		}

		return nil, connect.NewError(
			connect.CodeInternal,
			fmt.Errorf("forwarding request to agent %q: %w", agent.address, err),
		)
	}

	l.Info("request finished", "agent", agent.address)

	return res, nil
}

// Details forwards a Details request to the agent that controls the requested
// device and returns the agent's response.
//
//nolint:dupl // Commands and Details are parallel unary forwarders; dedup is tracked by the generic-forwarder TODO.
func (s *rpcService) Details(
	ctx context.Context,
	req *connect.Request[pb.DetailsRequest],
) (*connect.Response[pb.DetailsResponse], error) {
	device := req.Msg.GetDevice()
	ctx = log.With(log.WithScope(ctx, "rpc"), "rpc", "Details", "device", device)
	l := log.FromContext(ctx)
	l.Info("request received")

	agent, err := s.findAgent(device)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	res, err := forwardDetailsReq(log.WithScope(ctx, "relay"), agent.address, req)
	if err != nil {
		l.Error("forwarding to agent failed", "agent", agent.address, "err", err)

		// Preserve the downstream agent's status code when the failure is already a
		// connect error, instead of flattening every failure to CodeInternal.
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, err
		}

		return nil, connect.NewError(
			connect.CodeInternal,
			fmt.Errorf("forwarding request to agent %q: %w", agent.address, err),
		)
	}

	l.Info("request finished", "agent", agent.address)

	return res, nil
}

// errAgentClosedStream and errClientClosedStream are cancellation causes recorded
// on the relay's runCtx when a forwarding direction ends cleanly (io.EOF), so the
// completion log names which side closed the stream first.
var (
	errAgentClosedStream  = errors.New("agent closed the stream")
	errClientClosedStream = errors.New("client closed the stream")
)

// Run relays a client's bidirectional Run stream to the agent that controls the
// requested device, forwarding messages in both directions and bridging the
// client and agent version headers.
//
//nolint:cyclop,gocognit,funlen
func (s *rpcService) Run(
	ctx context.Context,
	downstream *connect.BidiStream[pb.RunRequest, pb.RunResponse],
) error {
	user := downstream.RequestHeader().Get(headers.User)

	// Set the RPC scope once; it flows to the relay forwarding goroutines on
	// ctx, so each logs only its own concern.
	ctx = log.With(log.WithScope(ctx, "rpc"), "rpc", "Run", "user", user)
	l := log.FromContext(ctx)
	l.Info("request received")

	downStreamRequest, err := downstream.Receive()
	if err != nil {
		return connect.NewError(
			connect.CodeAborted,
			fmt.Errorf("receiving request from client: %w", err),
		)
	}

	var (
		cmdMsg *pb.Command
		ok     bool
	)

	if cmdMsg, ok = isCommandMsg(downStreamRequest); !ok {
		return connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("first run request must contain a command"),
		)
	}

	device := cmdMsg.GetDevice()
	ctx = log.With(ctx, "device", device)
	l = log.FromContext(ctx)

	agent, err := s.findAgent(device)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}

	l.Info("routing to agent", "agent", agent.address)

	// Bind the whole relay exchange to runCtx so a forwarding goroutine exiting —
	// or this handler returning — tears down the upstream Run to the agent
	// promptly, carrying a cause for observability, instead of waiting for the
	// request context to unwind.
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	upstream := agent.conn(log.WithScope(runCtx, "relay")).Run(runCtx)

	// Forward the requesting user's identity to the agent so it can enforce locking.
	upstream.RequestHeader().Set(headers.User, user)

	// Relay the client's version to the agent (which enforces it); add none of our own.
	clientVersion := downstream.RequestHeader().Get(headers.Version)

	mismatch := checkMajorMismatch(clientVersion)
	if mismatch != nil {
		l.Warn("rejecting client: incompatible version", "client", clientVersion)

		return mismatch
	}

	upstream.RequestHeader().Set(headers.Version, clientVersion)

	// Forward the initial request to the DUT agent.
	err = upstream.Send(downStreamRequest)
	if err != nil {
		l.Error("forwarding to agent failed", "agent", agent.address, "err", err)

		// Preserve the downstream agent's status code when the failure is already a
		// connect error, instead of flattening every failure to CodeInternal.
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return err
		}

		return connect.NewError(
			connect.CodeInternal,
			fmt.Errorf("sending initial request to agent %q: %w", device, err),
		)
	}

	const numForwardingWorkers = 2

	errChan := make(chan error, numForwardingWorkers) // Each of the two goroutines can send an error.

	// relayAgentVersion forwards the agent's version to the client once, before the
	// first response, so the client's advisory check sees the real agent.
	var relayAgentVersion sync.Once

	// agent to client forwarding (downstream direction)
	go func() {
		defer cancel(nil)

		l := log.FromContext(log.WithScope(runCtx, "relay downstream"))

		for {
			select {
			case <-runCtx.Done():
				l.Debug("forwarding stopped: context cancelled")

				return
			default: // Unblock select, continue with the forwarding logic.
			}

			res, err := upstream.Receive()
			if errors.Is(err, io.EOF) {
				l.Debug("forwarding stopped: stream closed by agent")
				cancel(errAgentClosedStream)

				return
			}

			if err != nil {
				errChan <- err

				return
			}

			var versionErr error

			relayAgentVersion.Do(func() {
				agentVersion := upstream.ResponseHeader().Get(headers.Version)

				versionErr = checkMajorMismatch(agentVersion)
				if versionErr != nil {
					return
				}

				downstream.ResponseHeader().Set(headers.Version, agentVersion)
			})

			if versionErr != nil {
				errChan <- versionErr

				return
			}

			l.Debug("forwarding message to client", "kind", responseKind(res))

			err = downstream.Send(res)
			if err != nil {
				errChan <- err

				return
			}
		}
	}()

	// client to agent forwarding (upstream direction)
	go func() {
		defer cancel(nil)

		l := log.FromContext(log.WithScope(runCtx, "relay upstream"))

		for {
			select {
			case <-runCtx.Done():
				l.Debug("forwarding stopped: context cancelled")

				return
			default: // Unblock select, continue with the forwarding logic.
			}

			req, err := downstream.Receive()
			if errors.Is(err, io.EOF) {
				l.Debug("forwarding stopped: stream closed by client")
				cancel(errClientClosedStream)

				return
			}

			if err != nil {
				errChan <- err

				return
			}

			l.Debug("forwarding message to agent", "kind", requestKind(req))

			err = upstream.Send(req)
			if err != nil {
				errChan <- err

				return
			}
		}
	}()

	// Wait for both forwarding routines to finish, or for one to fail.
	select {
	case <-runCtx.Done():
		l.Info("request finished", "cause", context.Cause(runCtx))
	case err := <-errChan:
		l.Error("request finished with error", "err", err)

		return err
	}

	return nil
}

// isCommandMsg checks if the run request contains a command message and returns it if so.
func isCommandMsg(req *pb.RunRequest) (*pb.Command, bool) {
	if req == nil {
		return nil, false
	}

	cmdMsg := req.GetCommand()
	if cmdMsg == nil {
		return nil, false
	}

	return cmdMsg, true
}

// requestKind names the payload variant carried by a RunRequest, so relayed
// traffic can be logged by kind without dumping its contents.
func requestKind(req *pb.RunRequest) string {
	switch {
	case req.GetCommand() != nil:
		return "command"
	case req.GetConsole() != nil:
		return "console"
	case req.GetFileTransferRequest() != nil:
		return "file-transfer-request"
	case req.GetFileChunk() != nil:
		return "file-chunk"
	case req.GetFileTransferResponse() != nil:
		return "file-transfer-response"
	default:
		return "unknown"
	}
}

// responseKind names the payload variant carried by a RunResponse, so relayed
// traffic can be logged by kind without dumping its contents.
func responseKind(res *pb.RunResponse) string {
	switch {
	case res.GetPrint() != nil:
		return "print"
	case res.GetConsole() != nil:
		return "console"
	case res.GetFileTransferRequest() != nil:
		return "file-transfer-request"
	case res.GetFileChunk() != nil:
		return "file-chunk"
	case res.GetFileTransferResponse() != nil:
		return "file-transfer-response"
	default:
		return "unknown"
	}
}

// forwardCommandsReq forwards the Commands request to the respective DUT agent.
// It returns the response from the agent or an error if the request fails.
// TODO: try to refactor the forwarding functions into a generic function that can be reused for all RPCs.
func forwardCommandsReq(
	ctx context.Context,
	url string,
	req *connect.Request[pb.CommandsRequest],
) (*connect.Response[pb.CommandsResponse], error) {
	log.FromContext(ctx).Debug("forwarding commands request to agent", "agent", url)
	// TODO: potential resource leak. Investigate how clients can be reused or closed.
	// For now, we spawn a new client for each request.
	client := rpc.NewDeviceClient(url)

	// ctx carries the caller's cancellation and, since the dutctl client now sets a
	// per-call deadline that connect propagates as a grpc-timeout header, an
	// inherited deadline on request.Context() too. TODO(ctx): consider a
	// relay-owned bound independent of the inherited client deadline.
	return client.Commands(ctx, req)
}

// forwardDetailsReq forwards the Details request to the respective DUT agent.
// It returns the response from the agent or an error if the request fails.
// TODO: try to refactor the forwarding functions into a generic function that can be reused for all RPCs.
func forwardDetailsReq(
	ctx context.Context,
	url string,
	req *connect.Request[pb.DetailsRequest],
) (*connect.Response[pb.DetailsResponse], error) {
	log.FromContext(ctx).Debug("forwarding details request to agent", "agent", url)
	// TODO: potential resource leak. Investigate how clients can be reused or closed.
	// For now, we spawn a new client for each request.
	client := rpc.NewDeviceClient(url)

	// ctx carries the caller's cancellation and, since the dutctl client now sets a
	// per-call deadline that connect propagates as a grpc-timeout header, an
	// inherited deadline on request.Context() too. TODO(ctx): consider a
	// relay-owned bound independent of the inherited client deadline.
	return client.Details(ctx, req)
}

// checkMajorMismatch returns a CodeFailedPrecondition error when dutserver and a
// peer differ in major dutctl version, and nil otherwise. dutserver relays raw
// protobuf of its own schema and cannot bridge a major-version gap, so such a
// pairing is rejected rather than forwarded (mirroring the agent-side enforcer in
// internal/rpc).
func checkMajorMismatch(peer string) error {
	// Check the structural fact directly: dutserver can only bridge peers that
	// share its major schema version.
	if compat.Compare(buildinfo.Version, peer).Field != compat.Major {
		return nil
	}

	return connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("incompatible dutctl versions: dutserver %s, peer %q (major version mismatch)",
			buildinfo.Version, peer))
}

// Register records the devices served by a registering agent. If any device
// name is already registered the whole registration is rejected, so a partially
// applied registration is never left behind.
func (s *rpcService) Register(
	ctx context.Context,
	req *connect.Request[pb.RegisterRequest],
) (*connect.Response[pb.RegisterResponse], error) {
	ctx = log.With(log.WithScope(ctx, "rpc"), "rpc", "Register")
	l := log.FromContext(ctx)
	l.Info("request received")

	addr := req.Msg.GetAddress()
	if addr == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("agent address is not set"))
	}

	if len(req.Msg.GetDevices()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("empty device list"))
	}

	for i, dev := range req.Msg.GetDevices() {
		if dev == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("device %d has empty name", i+1))
		}
	}

	err := s.addAgent(log.WithScope(ctx, "registry"), addr, req.Msg.GetDevices())
	if err != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("adding agent %q: %w", addr, err))
	}

	res := connect.NewResponse(&pb.RegisterResponse{})

	l.Info("request finished")

	return res, nil
}
