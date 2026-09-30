// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/locker"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/pkg/module"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

const (
	testDevice  = "dev"
	testCommand = "cmd"
)

// clientStream is a session.Stream standing in for a live client. Receive hands
// out the queued requests and then blocks, as a client holding the stream open
// does, until the client hangs up. Send records every response, or fails with
// sendErr if set; sendFor makes each Send take that long, like a transport write
// that outlives its caller.
type clientStream struct {
	mu      sync.Mutex
	reqs    []*pb.RunRequest
	recvErr error
	sendErr error
	sent    []*pb.RunResponse

	sendFor  time.Duration
	inFlight atomic.Int32

	gone     chan struct{}
	hangOnce sync.Once
}

func newClientStream(t *testing.T, reqs ...*pb.RunRequest) *clientStream {
	t.Helper()

	s := &clientStream{reqs: reqs, gone: make(chan struct{})}
	t.Cleanup(s.hangUp)

	return s
}

// hangUp ends the stream from the client side: a blocked Receive returns.
func (s *clientStream) hangUp() {
	s.hangOnce.Do(func() { close(s.gone) })
}

func (s *clientStream) Receive() (*pb.RunRequest, error) {
	s.mu.Lock()

	if s.recvErr != nil {
		s.mu.Unlock()

		return nil, s.recvErr
	}

	if len(s.reqs) > 0 {
		req := s.reqs[0]
		s.reqs = s.reqs[1:]
		s.mu.Unlock()

		return req, nil
	}

	s.mu.Unlock()

	<-s.gone

	return nil, connect.NewError(connect.CodeCanceled, errors.New("client hung up"))
}

func (s *clientStream) Send(res *pb.RunResponse) error {
	s.inFlight.Add(1)
	defer s.inFlight.Add(-1)

	time.Sleep(s.sendFor)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sendErr != nil {
		return s.sendErr
	}

	s.sent = append(s.sent, res)

	return nil
}

// prints returns the text of every Print response sent so far.
func (s *clientStream) prints() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []string

	for _, res := range s.sent {
		if p := res.GetPrint(); p != nil {
			out = append(out, string(p.GetText()))
		}
	}

	return out
}

// funcModule adapts a function to module.Module, so each test states its
// module's behavior inline.
type funcModule func(ctx context.Context, s module.Session, args ...string) error

func (funcModule) Help() string                 { return "test module" }
func (funcModule) Init(context.Context) error   { return nil }
func (funcModule) Deinit(context.Context) error { return nil }

func (f funcModule) Run(ctx context.Context, s module.Session, args ...string) error {
	return f(ctx, s, args...)
}

// newRunService returns a service with one device offering one command made of
// mods; the first module is the passthrough module.
func newRunService(mods ...funcModule) *rpcService {
	var cmd dut.Command

	for i, m := range mods {
		mod := dut.Module{Module: m}
		mod.Config.Name = fmt.Sprintf("mod%d", i)
		mod.Config.Passthrough = i == 0
		cmd.Modules = append(cmd.Modules, mod)
	}

	return &rpcService{
		devices: dut.Devlist{testDevice: dut.Device{Cmds: map[string]dut.Command{testCommand: cmd}}},
		locker:  locker.New(),
	}
}

func commandReq(device, command string, args ...string) *pb.RunRequest {
	return &pb.RunRequest{Msg: &pb.RunRequest_Command{
		Command: &pb.Command{Device: device, Command: command, Args: args},
	}}
}

// deviceHold returns the effective hold on the test device, if any.
func deviceHold(svc *rpcService) (locker.Hold, bool) {
	hold, ok := svc.locker.StatusAll()[testDevice]

	return hold, ok
}

func TestRunSuccess(t *testing.T) {
	var busyDuringRun bool

	var svc *rpcService

	svc = newRunService(func(_ context.Context, s module.Session, args ...string) error {
		hold, ok := deviceHold(svc)
		busyDuringRun = ok && hold.Kind == locker.Busy

		s.Print("got ", args)

		return nil
	})

	stream := newClientStream(t, commandReq(testDevice, testCommand, "x"))

	err := svc.run(context.Background(), stream, "alice")
	if err != nil {
		t.Fatalf("run: unexpected error: %v", err)
	}

	if got := stream.prints(); len(got) != 1 || got[0] != "got [x]" {
		t.Errorf("client received %q, want [\"got [x]\"]", got)
	}

	if !busyDuringRun {
		t.Error("device not busy while its module ran")
	}

	if hold, ok := deviceHold(svc); ok {
		t.Errorf("auto-lock still held after run returned: %+v", hold)
	}
}

func TestRunRejectedBeforeModules(t *testing.T) {
	tests := []struct {
		name     string
		reqs     []*pb.RunRequest
		recvErr  error
		setup    func(t *testing.T, svc *rpcService)
		wantCode connect.Code
	}{
		{
			name:     "receive failure",
			recvErr:  errors.New("network down"),
			wantCode: connect.CodeAborted,
		},
		{
			name: "first message is not a command",
			reqs: []*pb.RunRequest{{Msg: &pb.RunRequest_Console{
				Console: &pb.Console{Data: &pb.Console_Stdin{Stdin: []byte("hi")}},
			}}},
			wantCode: connect.CodeInvalidArgument,
		},
		{
			name:     "unknown device",
			reqs:     []*pb.RunRequest{commandReq("ghost", testCommand)},
			wantCode: connect.CodeNotFound,
		},
		{
			name:     "unknown command",
			reqs:     []*pb.RunRequest{commandReq(testDevice, "ghost")},
			wantCode: connect.CodeNotFound,
		},
		{
			name: "device reserved by another user",
			reqs: []*pb.RunRequest{commandReq(testDevice, testCommand)},
			setup: func(t *testing.T, svc *rpcService) {
				t.Helper()

				if _, err := svc.locker.Lock(testDevice, "bob", time.Hour); err != nil {
					t.Fatalf("setup Lock: %v", err)
				}
			},
			wantCode: connect.CodeFailedPrecondition,
		},
		{
			name: "device busy with another user's run",
			reqs: []*pb.RunRequest{commandReq(testDevice, testCommand)},
			setup: func(t *testing.T, svc *rpcService) {
				t.Helper()

				if _, err := svc.locker.AutoLock(testDevice, "bob"); err != nil {
					t.Fatalf("setup AutoLock: %v", err)
				}
			},
			wantCode: connect.CodeFailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran atomic.Bool

			svc := newRunService(func(context.Context, module.Session, ...string) error {
				ran.Store(true)

				return nil
			})

			if tt.setup != nil {
				tt.setup(t, svc)
			}

			stream := newClientStream(t, tt.reqs...)
			stream.recvErr = tt.recvErr

			err := svc.run(context.Background(), stream, "alice")
			if connect.CodeOf(err) != tt.wantCode {
				t.Fatalf("code = %v (err = %v), want %v", connect.CodeOf(err), err, tt.wantCode)
			}

			if ran.Load() {
				t.Error("module ran although the run was rejected")
			}
		})
	}
}

func TestRunArgsWithoutReceiver(t *testing.T) {
	var ran atomic.Bool

	svc := newRunService(func(context.Context, module.Session, ...string) error {
		ran.Store(true)

		return nil
	})

	// With no passthrough module and no declared args, runtime args have no
	// receiver (dut.ErrNoReceiverForArgs).
	cmd := svc.devices[testDevice].Cmds[testCommand]
	cmd.Modules[0].Config.Passthrough = false
	svc.devices[testDevice].Cmds[testCommand] = cmd

	stream := newClientStream(t, commandReq(testDevice, testCommand, "stray"))

	err := svc.run(context.Background(), stream, "alice")
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v (err = %v), want InvalidArgument", connect.CodeOf(err), err)
	}

	if ran.Load() {
		t.Error("module ran despite unresolvable arguments")
	}

	if hold, ok := deviceHold(svc); ok {
		t.Errorf("auto-lock still held after run returned: %+v", hold)
	}
}

func TestRunModuleFailureReleasesDevice(t *testing.T) {
	tests := []struct {
		name string
		mod  funcModule
	}{
		{
			name: "module returns an error",
			mod: func(context.Context, module.Session, ...string) error {
				return errors.New("flash tool exited with code 1")
			},
		},
		{
			name: "module panics",
			mod: func(context.Context, module.Session, ...string) error {
				panic("module bug")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newRunService(tt.mod)
			stream := newClientStream(t, commandReq(testDevice, testCommand))

			err := svc.run(context.Background(), stream, "alice")
			if connect.CodeOf(err) != connect.CodeAborted {
				t.Fatalf("code = %v (err = %v), want Aborted", connect.CodeOf(err), err)
			}

			if hold, ok := deviceHold(svc); ok {
				t.Errorf("auto-lock still held after run returned: %+v", hold)
			}
		})
	}
}

// A module that succeeds does not make a run succeed whose output never
// reached the client.
func TestRunFailsOnBrokenStream(t *testing.T) {
	svc := newRunService(func(_ context.Context, s module.Session, _ ...string) error {
		s.Print("lost")

		return nil
	})

	stream := newClientStream(t, commandReq(testDevice, testCommand))
	stream.sendErr = errors.New("connection reset")

	err := svc.run(context.Background(), stream, "alice")
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v (err = %v), want Internal", connect.CodeOf(err), err)
	}

	if hold, ok := deviceHold(svc); ok {
		t.Errorf("auto-lock still held after run returned: %+v", hold)
	}
}

func TestRunStopsAtFirstFailingModule(t *testing.T) {
	var secondRan atomic.Bool

	svc := newRunService(
		func(context.Context, module.Session, ...string) error { return errors.New("first failed") },
		func(context.Context, module.Session, ...string) error {
			secondRan.Store(true)

			return nil
		},
	)

	stream := newClientStream(t, commandReq(testDevice, testCommand))

	err := svc.run(context.Background(), stream, "alice")
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("code = %v (err = %v), want Aborted", connect.CodeOf(err), err)
	}

	if secondRan.Load() {
		t.Error("second module ran after the first failed")
	}
}

func TestRunKeepsSameUsersReservation(t *testing.T) {
	svc := newRunService(func(context.Context, module.Session, ...string) error { return nil })

	if _, err := svc.locker.Lock(testDevice, "alice", time.Hour); err != nil {
		t.Fatalf("setup Lock: %v", err)
	}

	stream := newClientStream(t, commandReq(testDevice, testCommand))

	if err := svc.run(context.Background(), stream, "alice"); err != nil {
		t.Fatalf("run: unexpected error: %v", err)
	}

	hold, ok := deviceHold(svc)
	if !ok || hold.Kind != locker.Reserved {
		t.Fatalf("hold = %+v (ok=%v), want alice's reservation intact", hold, ok)
	}

	// Releasing the reservation must leave the device free, proving the run's
	// Busy hold was cleared rather than merely shadowed by the reservation.
	if err := svc.locker.ClearLock(testDevice, "alice"); err != nil {
		t.Fatalf("ClearLock: %v", err)
	}

	if hold, ok := deviceHold(svc); ok {
		t.Errorf("Busy hold still present after the run: %+v", hold)
	}
}

func TestRunCancellation(t *testing.T) {
	t.Run("client hangs up during the module", func(t *testing.T) {
		started := make(chan struct{})

		svc := newRunService(func(ctx context.Context, _ module.Session, _ ...string) error {
			close(started)
			<-ctx.Done()

			return ctx.Err()
		})

		stream := newClientStream(t, commandReq(testDevice, testCommand))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			<-started
			cancel()
			stream.hangUp()
		}()

		err := svc.run(ctx, stream, "alice")
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Fatalf("code = %v (err = %v), want Canceled", connect.CodeOf(err), err)
		}
	})

	t.Run("deadline passes during the module", func(t *testing.T) {
		svc := newRunService(func(ctx context.Context, _ module.Session, _ ...string) error {
			<-ctx.Done()

			return ctx.Err()
		})

		stream := newClientStream(t, commandReq(testDevice, testCommand))

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		err := svc.run(ctx, stream, "alice")
		if connect.CodeOf(err) != connect.CodeDeadlineExceeded {
			t.Fatalf("code = %v (err = %v), want DeadlineExceeded", connect.CodeOf(err), err)
		}
	})

	t.Run("cancelled before the run starts", func(t *testing.T) {
		var ran atomic.Bool

		svc := newRunService(func(context.Context, module.Session, ...string) error {
			ran.Store(true)

			return nil
		})

		stream := newClientStream(t, commandReq(testDevice, testCommand))

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := svc.run(ctx, stream, "alice")
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Fatalf("code = %v (err = %v), want Canceled", connect.CodeOf(err), err)
		}

		if ran.Load() {
			t.Error("module ran on a cancelled request")
		}

		if hold, ok := deviceHold(svc); ok {
			t.Errorf("auto-lock still held after run returned: %+v", hold)
		}
	})
}

// TestRunNoSendOutlivesRun guards the agent crash where the handler returned
// while a broker worker was still inside a stream Send: connect invalidates the
// response writer once the handler is gone, and the late write panics in a
// goroutine no recover covers. The module prints and then fails, so the run
// ends while the printed message is still being sent.
func TestRunNoSendOutlivesRun(t *testing.T) {
	svc := newRunService(func(_ context.Context, s module.Session, _ ...string) error {
		s.Print("flash tool output")

		return errors.New("flash tool exited with code 1")
	})

	stream := newClientStream(t, commandReq(testDevice, testCommand))
	stream.sendFor = 100 * time.Millisecond

	err := svc.run(context.Background(), stream, "alice")
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("code = %v (err = %v), want Aborted", connect.CodeOf(err), err)
	}

	if inFlight := stream.inFlight.Load(); inFlight != 0 {
		t.Errorf("%d stream send(s) still in flight after run returned", inFlight)
	}

	if got := stream.prints(); len(got) != 1 {
		t.Errorf("client received %q, want the module's one message", got)
	}
}
