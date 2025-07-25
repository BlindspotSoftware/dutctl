// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"slices"
	"sync"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
	"golang.org/x/net/http2"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

type agent struct {
	// address is the address of the DUT agent.
	address string
	// client is the Connect RPC client for the DUT agent.
	// Do not use this client directly, but use agent's conn method.
	client dutctlv1connect.DeviceServiceClient

	sync.Mutex
}

// conn returns the RPC client that connetcs to the DUT agent.
//
//nolint:ireturn
func (a *agent) conn() dutctlv1connect.DeviceServiceClient {
	a.Lock()
	defer a.Unlock()

	if a.client == nil {
		a.client = spawnClient(a.address)
	}

	return a.client
}

// rpcService is the service implementation for the RPCs provided by dutserver.
// It implements both, the DeviceService used by clients as they would use with dutagents
// and the RelayService used by agents to register with the server.
type rpcService struct {
	// agents holds handles of the registered DUT agents.
	agents map[string]*agent

	sync.RWMutex
}

// findAgent returns the handle for the DUT agent, that controls the device with the given name.
func (s *rpcService) findAgent(device string) (*agent, error) {
	s.RLock()
	defer s.RUnlock()

	if agent, ok := s.agents[device]; ok {
		return agent, nil
	}

	return nil, errors.New("device not found: " + device)
}

// addAgent tries to register devices handled by an agent with address.
// If one of the provided devices already exists an error is returned and none of the deviced will be stored.
func (s *rpcService) addAgent(address string, devices []string) error {
	s.Lock()
	defer s.Unlock()

	for _, device := range devices {
		if _, exists := s.agents[device]; exists {
			return fmt.Errorf("device %q already registered", device)
		}
	}

	for _, device := range devices {
		s.agents[device] = &agent{address: address}
	}

	return nil
}

// List is the handler for the List RPC.
func (s *rpcService) List(
	_ context.Context,
	_ *connect.Request[pb.ListRequest],
) (*connect.Response[pb.ListResponse], error) {
	log.Println("Server received List request")

	res := connect.NewResponse(&pb.ListResponse{
		Devices: slices.Sorted(maps.Keys(s.agents)),
	})

	log.Print("List-RPC finished")

	return res, nil
}

// Commands is the handler for the Commands RPC.
func (s *rpcService) Commands(
	ctx context.Context,
	req *connect.Request[pb.CommandsRequest],
) (*connect.Response[pb.CommandsResponse], error) {
	log.Println("Server received Commands request")

	device := req.Msg.GetDevice()

	agent, err := s.findAgent(device)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	res, err := forwardCommandsReq(ctx, agent.address, req)
	if err != nil {
		return nil, connect.NewError(
			connect.CodeInternal,
			fmt.Errorf("forwarding request to agent %q: %w", agent.address, err),
		)
	}

	log.Print("Commands-RPC finished")

	return res, nil
}

func (s *rpcService) Details(
	ctx context.Context,
	req *connect.Request[pb.DetailsRequest],
) (*connect.Response[pb.DetailsResponse], error) {
	log.Println("Server received Details request")

	device := req.Msg.GetDevice()

	agent, err := s.findAgent(device)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	res, err := forwardDetailsReq(ctx, agent.address, req)
	if err != nil {
		return nil, connect.NewError(
			connect.CodeInternal,
			fmt.Errorf("forwarding request to agent %q: %w", agent.address, err),
		)
	}

	log.Print("Details-RPC finished")

	return res, nil
}

// Run is the handler for the Run RPC.
//
//nolint:cyclop,gocognit,funlen
func (s *rpcService) Run(
	ctx context.Context,
	downstream *connect.BidiStream[pb.RunRequest, pb.RunResponse],
) error {
	log.Println("Server received Run request")

	donwnStreamRequest, err := downstream.Receive()
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

	if cmdMsg, ok = isCommandMsg(donwnStreamRequest); !ok {
		return connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("first run request must contain a command"),
		)
	}

	device := cmdMsg.GetDevice()

	agent, err := s.findAgent(device)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	upstream := agent.conn().Run(ctx)

	// This is the first message of a new Run RPC from a client.
	log.Println("Run request has a command message - starting new stream to DUT agent")

	// Forward the initial request to the DUT agent.
	if err := upstream.Send(donwnStreamRequest); err != nil {
		return connect.NewError(
			connect.CodeInternal,
			fmt.Errorf("sending initial request to agent %q: %w", device, err),
		)
	}

	// TODO: consider refactoring and use context.WithCancelCause(ctx)
	const numForwardingWorkers = 2
	errChan := make(chan error, numForwardingWorkers) // Each oft the two goroutines can send an error.

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel() // Ensure the context is cancelled when the function returns.

	// agent to client forwarding
	go func() {
		defer cancel()

		for {
			select {
			case <-runCtx.Done():
				log.Println("Agent to client forwarding terminating: Run-Context cancelled")

				return
			default: // Unblock select, continue with the forwarding logic.
			}

			log.Println("Receiving response from agent to forward to client (blocking)")

			res, err := upstream.Receive()
			if errors.Is(err, io.EOF) {
				log.Println("Agent to client forwarding terminating: Stream closed by agent")

				return
			}

			if err != nil {
				log.Printf("Agent to client forwarding terminating as receiving has been cancelled: %v", err)
				errChan <- err

				return
			}

			log.Printf("Forwarding response to client: %v", res)

			if err := downstream.Send(res); err != nil {
				log.Printf("Agent to client forwarding terminating as sending has been cancelled: %v", err)
				errChan <- err

				return
			}
		}
	}()

	// client to agent forwarding
	go func() {
		defer cancel()

		for {
			select {
			case <-runCtx.Done():
				log.Println("Client to agent forwarding terminating: Run-Context cancelled")

				return
			default: // Unblock select, continue with the forwarding logic.
			}

			log.Println("Receiving request from client to forward to agent (blocking)")

			req, err := downstream.Receive()
			if errors.Is(err, io.EOF) {
				log.Println("Client to agent forwarding terminating: Stream closed by client")

				return
			}

			if err != nil {
				log.Printf("Client to agent forwarding terminating as receiving has been cancelled: %v", err)
				errChan <- err

				return
			}

			log.Printf("Forwarding request to agent %q: %v", device, req)

			if err := upstream.Send(req); err != nil {
				log.Printf("Client to agent forwarding terminating as sending has been cancelled: %v", err)
				errChan <- err

				return
			}
		}
	}()

	// Wait for both forwarding routines to finish.
	log.Println("Waiting for forwarding routines to finish")

	// Check if any of the forwarding routines encountered an error.
	select {
	case <-runCtx.Done():
		log.Println("Run RPC forwarding completed successfully")
	case err := <-errChan:
		log.Printf("Run RPC forwarding aborted, forwarding error: %v", err)

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

// forwardCommandsReq forwards the Commands request to the respective DUT agent.
// It returns the response from the agent or an error if the request fails.
// TODO: try to refactor the forwarding functions into a generic function that can be reused for all RPCs.
func forwardCommandsReq(
	ctx context.Context,
	url string,
	req *connect.Request[pb.CommandsRequest],
) (*connect.Response[pb.CommandsResponse], error) {
	log.Printf("Forwarding Commands request to agent %q", url)
	// TODO: potential resource leak. Investigate how clients can be reused or closed.
	// For now, we spawn a new client for each request.
	client := spawnClient(url)

	return client.Commands(ctx, req)
}

// forwardDetailsReq forwards the Details request to the respective DUT agent.
// It returns the response from the agent or an error if the request fails.
// TODO: try to refactor the forwarding functions into a generic function that can be reused for all RPCs.
func forwardDetailsReq(
	ctx context.Context,
	agent string,
	req *connect.Request[pb.DetailsRequest],
) (*connect.Response[pb.DetailsResponse], error) {
	log.Printf("Forwarding Details request to agent %q", agent)
	// TODO: potential resource leak. Investigate how clients can be reused or closed.
	// For now, we spawn a new client for each request.
	client := spawnClient(agent)

	return client.Details(ctx, req)
}

// spawnClient creates a new client to the DUT agent specified by the agent address.
// TODO: refactor into pkg and reuse in dutctl and dutserver.
//
//nolint:ireturn
func spawnClient(agendURL string) dutctlv1connect.DeviceServiceClient {
	log.Printf("Spawning new client for agent %q", agendURL)

	return dutctlv1connect.NewDeviceServiceClient(
		// Instead of http.DefaultClient, use the HTTP/2 protocol without TLS
		newInsecureClient(),
		fmt.Sprintf("http://%s", agendURL),
		connect.WithGRPC(),
	)
}

// TODO: refactor into pkg and reuse in dutctl and dutserver.
func newInsecureClient() *http.Client {
	return &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				// If you're also using this client for non-h2c traffic, you may want
				// to delegate to tls.Dial if the network isn't TCP or the addr isn't
				// in an allowlist.
				return net.Dial(network, addr)
			},
			// TODO: Don't forget timeouts!
		},
	}
}

func (s *rpcService) Register(
	_ context.Context,
	req *connect.Request[pb.RegisterRequest],
) (*connect.Response[pb.RegisterResponse], error) {
	log.Println("Server received Register request")

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

	log.Printf("Registering agent %q with devices %v", addr, req.Msg.GetDevices())

	if err := s.addAgent(addr, req.Msg.GetDevices()); err != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("adding agent %q: %w", addr, err))
	}

	res := connect.NewResponse(&pb.RegisterResponse{})

	log.Print("Register-RPC finished")

	return res, nil
}
