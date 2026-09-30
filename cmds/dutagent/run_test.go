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
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/session"
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

// The arguments are resolved before the auto-lock is taken, so a request that
// cannot run reports its own fault even on a device someone else holds, and
// never touches the lock.
func TestRunArgsResolvedBeforeLocking(t *testing.T) {
	svc := newRunService(func(context.Context, module.Session, ...string) error { return nil })

	cmd := svc.devices[testDevice].Cmds[testCommand]
	cmd.Modules[0].Config.Passthrough = false
	svc.devices[testDevice].Cmds[testCommand] = cmd

	if _, err := svc.locker.Lock(testDevice, "bob", time.Hour); err != nil {
		t.Fatalf("setup Lock: %v", err)
	}

	stream := newClientStream(t, commandReq(testDevice, testCommand, "stray"))

	err := svc.run(context.Background(), stream, "alice")
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v (err = %v), want InvalidArgument", connect.CodeOf(err), err)
	}

	if hold, ok := deviceHold(svc); !ok || hold.Owner != "bob" || hold.Kind != locker.Reserved {
		t.Errorf("hold = %+v (ok=%v), want bob's reservation untouched", hold, ok)
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

func TestFindCommand(t *testing.T) {
	// makeDevlist builds a device offering one command of n modules, of which
	// the first passthrough are marked passthrough (more than one makes the
	// command invalid).
	makeDevlist := func(n, passthrough int) dut.Devlist {
		modules := make([]dut.Module, 0, n)

		for i := range n {
			mod := dut.Module{}
			mod.Config.Name = fmt.Sprintf("mod%d", i)
			mod.Config.Passthrough = i < passthrough
			modules = append(modules, mod)
		}

		return dut.Devlist{testDevice: dut.Device{Cmds: map[string]dut.Command{testCommand: {Modules: modules}}}}
	}

	tests := []struct {
		name     string
		devs     dut.Devlist
		device   string
		command  string
		wantCode connect.Code // 0 means success
	}{
		{name: "found", devs: makeDevlist(1, 1), device: testDevice, command: testCommand},
		{name: "unknown device", devs: makeDevlist(1, 1), device: "ghost", command: testCommand, wantCode: connect.CodeNotFound},
		{name: "unknown command", devs: makeDevlist(1, 1), device: testDevice, command: "ghost", wantCode: connect.CodeNotFound},
		{name: "command without modules", devs: makeDevlist(0, 0), device: testDevice, command: testCommand, wantCode: connect.CodeInternal},
		{name: "two passthrough modules", devs: makeDevlist(2, 2), device: testDevice, command: testCommand, wantCode: connect.CodeInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := findCommand(tt.devs, tt.device, tt.command)

			if tt.wantCode != 0 {
				if connect.CodeOf(err) != tt.wantCode {
					t.Fatalf("code = %v (err = %v), want %v", connect.CodeOf(err), err, tt.wantCode)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(cmd.Modules) != 1 {
				t.Errorf("found command with %d modules, want 1", len(cmd.Modules))
			}
		})
	}
}

func TestClearAutoLock(t *testing.T) {
	t.Run("clears_auto_slot_only", func(t *testing.T) {
		l := locker.New()
		if _, err := l.Lock(testDevice, "alice", time.Hour); err != nil {
			t.Fatalf("setup Lock: %v", err)
		}

		if _, err := l.AutoLock(testDevice, "alice"); err != nil {
			t.Fatalf("setup AutoLock: %v", err)
		}

		clearAutoLock(context.Background(), l, testDevice, "alice")

		got, ok := l.StatusAll()[testDevice]
		if !ok || got.Kind != locker.Reserved {
			t.Errorf("StatusAll[%s] = %+v (ok=%v), want the reservation intact", testDevice, got, ok)
		}

		// Releasing the reservation must leave the device free, proving the Busy
		// hold really was cleared rather than merely shadowed by the reservation.
		if err := l.ClearLock(testDevice, "alice"); err != nil {
			t.Fatalf("ClearLock: %v", err)
		}

		if _, ok := l.StatusAll()[testDevice]; ok {
			t.Error("Busy hold still present after clearAutoLock")
		}
	})

	t.Run("missing_auto_lock_is_tolerated", func(t *testing.T) {
		l := locker.New()

		// Nothing held: ClearAutoLock returns ErrNotLocked, which clearAutoLock
		// swallows. The test asserts it neither panics nor fails.
		clearAutoLock(context.Background(), l, testDevice, "alice")
	})
}

func TestRunModules(t *testing.T) {
	// recorder returns a module that records the args it ran with and returns err.
	recorder := func(got *[]string, runs *int, err error) dut.Module {
		mod := dut.Module{Module: funcModule(func(_ context.Context, _ module.Session, args ...string) error {
			*runs++
			*got = append([]string{}, args...)

			return err
		})}
		mod.Config.Name = "recorder"

		return mod
	}

	t.Run("each module gets its own args", func(t *testing.T) {
		var firstArgs, secondArgs []string

		var firstRuns, secondRuns int

		mods := []dut.Module{recorder(&firstArgs, &firstRuns, nil), recorder(&secondArgs, &secondRuns, nil)}

		err := runModules(context.Background(), nil, mods, [][]string{{"x", "y"}, {"conf1"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if firstRuns != 1 || fmt.Sprint(firstArgs) != "[x y]" {
			t.Errorf("first module: runs=%d args=%v, want 1 run with [x y]", firstRuns, firstArgs)
		}

		if secondRuns != 1 || fmt.Sprint(secondArgs) != "[conf1]" {
			t.Errorf("second module: runs=%d args=%v, want 1 run with [conf1]", secondRuns, secondArgs)
		}
	})

	t.Run("stops at the first failure", func(t *testing.T) {
		var firstArgs, secondArgs []string

		var firstRuns, secondRuns int

		failure := errors.New("helper failed")
		mods := []dut.Module{recorder(&firstArgs, &firstRuns, failure), recorder(&secondArgs, &secondRuns, nil)}

		err := runModules(context.Background(), nil, mods, [][]string{nil, nil})
		if !errors.Is(err, failure) {
			t.Fatalf("err = %v, want %v", err, failure)
		}

		if firstRuns != 1 || secondRuns != 0 {
			t.Errorf("runs = %d, %d, want 1, 0", firstRuns, secondRuns)
		}
	})

	t.Run("runs nothing once cancelled", func(t *testing.T) {
		var args []string

		var runs int

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := runModules(ctx, nil, []dut.Module{recorder(&args, &runs, nil)}, [][]string{nil})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}

		if runs != 0 {
			t.Errorf("module ran %d time(s) on a cancelled context", runs)
		}
	})
}

func TestWaitModules(t *testing.T) {
	boom := errors.New("boom")

	tests := []struct {
		name      string
		cancelled bool
		moduleErr []error // values sent on the module channel, in order
		brokerErr []error // values sent on the broker channel, which is then closed
		wantCode  connect.Code
	}{
		{name: "modules and broker succeed", moduleErr: []error{nil}},
		{name: "context cancelled", cancelled: true, wantCode: connect.CodeCanceled},
		{name: "module fails", moduleErr: []error{boom}, wantCode: connect.CodeAborted},
		{name: "broker fails", brokerErr: []error{boom}, wantCode: connect.CodeInternal},
		{name: "broker rejects a file transfer", brokerErr: []error{session.ErrBadFileTransfer}, wantCode: connect.CodeInvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if tt.cancelled {
				cancel()
			}

			moduleErrCh := make(chan error, 1)
			for _, err := range tt.moduleErr {
				moduleErrCh <- err
			}

			brokerErrCh := make(chan error, 2)
			for _, err := range tt.brokerErr {
				brokerErrCh <- err
			}

			close(brokerErrCh)

			// A case that never reports on the module channel must still
			// end: its outcome comes from the broker channel or the context.
			err := waitModules(ctx, moduleErrCh, brokerErrCh)

			if tt.wantCode == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				return
			}

			if connect.CodeOf(err) != tt.wantCode {
				t.Fatalf("code = %v (err = %v), want %v", connect.CodeOf(err), err, tt.wantCode)
			}
		})
	}
}
