// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/locker"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/session"
	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/pkg/module"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

const (
	testDevice  = "dev"
	testCommand = "cmd"
)

// clientStream is a session.Stream standing in for a live client. Receive hands
// out the queued requests, then those pushed later, and otherwise blocks, as a
// client holding the stream open does, until the client hangs up. Send records
// every response, or fails with sendErr if set; sendFor makes each Send take
// that long, like a transport write that outlives its caller.
type clientStream struct {
	mu      sync.Mutex
	reqs    []*pb.RunRequest
	recvErr error
	sendErr error
	sent    []*pb.RunResponse

	sendFor  time.Duration
	inFlight atomic.Int32

	pushed   chan *pb.RunRequest
	gone     chan struct{}
	hangOnce sync.Once
}

func newClientStream(t *testing.T, reqs ...*pb.RunRequest) *clientStream {
	t.Helper()

	s := &clientStream{reqs: reqs, pushed: make(chan *pb.RunRequest, 1), gone: make(chan struct{})}
	t.Cleanup(s.hangUp)

	return s
}

// push sends req from the client while the run is under way.
func (s *clientStream) push(req *pb.RunRequest) {
	s.pushed <- req
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

	select {
	case req := <-s.pushed:
		return req, nil
	case <-s.gone:
		return nil, connect.NewError(connect.CodeCanceled, errors.New("client hung up"))
	}
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
		{
			name: "device busy with the same user's run",
			reqs: []*pb.RunRequest{commandReq(testDevice, testCommand)},
			setup: func(t *testing.T, svc *rpcService) {
				t.Helper()

				if _, err := svc.locker.AutoLock(testDevice, "alice"); err != nil {
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

// A device runs one command at a time, even for one user: a second run of the
// same user, e.g. a power cycle from another terminal while a console is open,
// is rejected as the device being busy, without presenting the user as a
// stranger holding it. The first run keeps the device until it ends.
func TestRunSameUserSecondRunRejected(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})

	var secondRan atomic.Bool

	svc := newRunService(func(_ context.Context, _ module.Session, args ...string) error {
		if fmt.Sprint(args) == "[long]" {
			close(started)
			<-release

			return nil
		}

		secondRan.Store(true)

		return nil
	})

	long := newClientStream(t, commandReq(testDevice, testCommand, "long"))
	done := make(chan error, 1)

	go func() { done <- svc.run(context.Background(), long, "alice") }()

	select {
	case <-started:
	case err := <-done:
		t.Fatalf("alice's first run returned before its module started: %v", err)
	}

	err := svc.run(context.Background(), newClientStream(t, commandReq(testDevice, testCommand)), "alice")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !errors.Is(err, locker.ErrAlreadyRunning) {
		t.Errorf("alice's second run: code = %v (err = %v), want FailedPrecondition matching ErrAlreadyRunning", connect.CodeOf(err), err)
	}

	want := `device "dev" is still running a command for "alice"; a cancelled command keeps the device until it has stopped`
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Message() != want {
		t.Errorf("alice's second run: err = %v, want the message %q", err, want)
	}

	if secondRan.Load() {
		t.Error("alice's second run ran its module while the first still ran")
	}

	err = svc.run(context.Background(), newClientStream(t, commandReq(testDevice, testCommand)), "bob")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !errors.Is(err, locker.ErrWrongOwner) {
		t.Errorf("bob's run: code = %v (err = %v), want FailedPrecondition matching ErrWrongOwner", connect.CodeOf(err), err)
	}

	hold, ok := deviceHold(svc)
	if !ok || hold.Owner != "alice" || hold.Kind != locker.Busy {
		t.Errorf("hold = %+v (ok=%v) while alice's first run is still going, want alice's Busy hold", hold, ok)
	}

	close(release)

	if err := <-done; err != nil {
		t.Fatalf("alice's first run: unexpected error: %v", err)
	}

	if hold, ok := deviceHold(svc); ok {
		t.Errorf("auto-lock still held after the first run returned: %+v", hold)
	}
}

// A forced unlock breaks a reservation, never a running command. While alice's
// run is going, bob's forced unlock releases her reservation but leaves the
// device busy: bob's run is turned away, and a second forced unlock finds only
// the command and fails. Once alice's run has returned, the device is free and
// bob's run is admitted.
func TestRunForcedUnlockKeepsDeviceBusy(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})

	var bobRan atomic.Bool

	svc := newRunService(func(_ context.Context, _ module.Session, args ...string) error {
		if fmt.Sprint(args) == "[alice]" {
			close(started)
			<-release

			return nil
		}

		bobRan.Store(true)

		return nil
	})

	if _, err := svc.locker.Lock(testDevice, "alice", time.Hour); err != nil {
		t.Fatalf("setup Lock: %v", err)
	}

	alices := newClientStream(t, commandReq(testDevice, testCommand, "alice"))
	done := make(chan error, 1)

	go func() { done <- svc.run(context.Background(), alices, "alice") }()

	select {
	case <-started:
	case err := <-done:
		t.Fatalf("alice's run returned before its module started: %v", err)
	}

	if _, err := svc.Unlock(userCtx("bob"), unlockReq(testDevice, true)); err != nil {
		t.Fatalf("bob's forced unlock of alice's reservation: unexpected error: %v", err)
	}

	err := svc.run(context.Background(), newClientStream(t, commandReq(testDevice, testCommand, "bob")), "bob")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !errors.Is(err, locker.ErrWrongOwner) {
		t.Errorf("bob's run after the forced unlock: code = %v (err = %v), want FailedPrecondition matching ErrWrongOwner",
			connect.CodeOf(err), err)
	}

	if bobRan.Load() {
		t.Error("bob's module ran while alice's run was still going")
	}

	_, err = svc.Unlock(userCtx("bob"), unlockReq(testDevice, true))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !errors.Is(err, locker.ErrBusy) {
		t.Errorf("bob's second forced unlock: code = %v (err = %v), want FailedPrecondition matching ErrBusy", connect.CodeOf(err), err)
	}

	close(release)

	if err := <-done; err != nil {
		t.Fatalf("alice's run: unexpected error: %v", err)
	}

	if hold, ok := deviceHold(svc); ok {
		t.Fatalf("hold = %+v after alice's run returned, want the device free", hold)
	}

	if err := svc.run(context.Background(), newClientStream(t, commandReq(testDevice, testCommand, "bob")), "bob"); err != nil {
		t.Fatalf("bob's run on the freed device: unexpected error: %v", err)
	}

	if !bobRan.Load() {
		t.Error("bob's run on the freed device did not run its module")
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

	// A subprocess module stopped by SIGTERM fails with the tool's own error, not
	// ctx.Err(); the run is still reported as cancelled, not as a module failure.
	t.Run("module fails with its own error after the hang-up", func(t *testing.T) {
		started := make(chan struct{})

		svc := newRunService(func(ctx context.Context, _ module.Session, _ ...string) error {
			close(started)
			<-ctx.Done()

			return errors.New("flash tool exited: signal: terminated")
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

	t.Run("missing_auto_lock_is_logged", func(t *testing.T) {
		l := locker.New()

		var buf bytes.Buffer

		ctx := log.Into(context.Background(), slog.New(slog.NewTextHandler(&buf, nil)))

		// Nothing held: a device runs one command at a time, so no other run
		// shares the hold, and a forced unlock no longer ends it. The
		// ErrNotLocked from ClearAutoLock is therefore unexpected, and
		// clearAutoLock warns about it instead of passing over it.
		clearAutoLock(ctx, l, testDevice, "alice")

		if out := buf.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "failed to release auto-lock") {
			t.Errorf("log = %q, want a warning that the auto-lock could not be released", out)
		}
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

	// A module that stops because its command was cancelled did not fail on
	// its own: it is logged as a warning, not as an error.
	t.Run("a module stopped by a cancellation logs a warning", func(t *testing.T) {
		var buf bytes.Buffer

		ctx, cancel := context.WithCancel(log.Into(context.Background(), slog.New(slog.NewTextHandler(&buf, nil))))
		defer cancel()

		mod := dut.Module{Module: funcModule(func(ctx context.Context, _ module.Session, _ ...string) error {
			cancel()
			<-ctx.Done()

			return errors.New("flash tool exited: signal: terminated")
		})}
		mod.Config.Name = "stopped"

		err := runModules(ctx, nil, []dut.Module{mod}, [][]string{nil})
		if err == nil {
			t.Fatal("runModules returned nil for a module that returned an error")
		}

		out := buf.String()
		if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "module stopped after the command was cancelled") ||
			strings.Contains(out, "level=ERROR") {
			t.Errorf("log = %q, want a warning that the module stopped after the cancellation, and no error", out)
		}
	})
}

// TestRunHoldsDeviceUntilModulesReturn guards the device against being handed
// on while a module still drives it. A module that honors ctx can still need
// time to stop — procexec gives a subprocess a grace period after SIGTERM — so
// the run must end, and release its auto-lock, only once the module returned,
// on every path that cancels it.
func TestRunHoldsDeviceUntilModulesReturn(t *testing.T) {
	tests := []struct {
		name string
		// send is what the client sends once the module runs; nil means the
		// client cancels and hangs up instead.
		send      *pb.RunRequest
		wantCode  connect.Code
		wantCause error // the cause the module's context reports
	}{
		{
			name:      "client hangs up",
			wantCode:  connect.CodeCanceled,
			wantCause: context.Canceled,
		},
		{
			// A file nobody requested is a protocol violation that fails the
			// upstream worker, which aborts the modules.
			name: "client breaks the file protocol",
			send: &pb.RunRequest{Msg: &pb.RunRequest_File{
				File: &pb.File{Path: "unrequested", Content: []byte("x")},
			}},
			wantCode:  connect.CodeInvalidArgument,
			wantCause: session.ErrBadFileTransfer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			started, stopping, release := make(chan struct{}), make(chan struct{}), make(chan struct{})

			var (
				svc         *rpcService
				cause       error
				heldAtStop  bool
				releaseOnce sync.Once
			)

			svc = newRunService(func(ctx context.Context, _ module.Session, _ ...string) error {
				close(started)
				<-ctx.Done()
				cause = context.Cause(ctx)
				close(stopping)

				<-release // still stopping, like a subprocess in its grace period

				hold, ok := deviceHold(svc)
				heldAtStop = ok && hold.Owner == "alice"

				return ctx.Err()
			})

			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

			stream := newClientStream(t, commandReq(testDevice, testCommand))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan error, 1)

			go func() { done <- svc.run(ctx, stream, "alice") }()

			select {
			case <-started:
			case err := <-done:
				t.Fatalf("run returned before its module started: %v", err)
			}

			if tt.send != nil {
				stream.push(tt.send)
			} else {
				cancel()
				stream.hangUp()
			}

			select {
			case <-stopping:
			case <-time.After(time.Second):
				t.Fatal("the module's context was not cancelled")
			}

			// While the module is stopping, the run must not end. A slow runner
			// can only make this pass by mistake, never fail by mistake.
			select {
			case err := <-done:
				t.Fatalf("run returned (%v) while its module was still running", err)
			case <-time.After(50 * time.Millisecond):
			}

			if _, ok := deviceHold(svc); !ok {
				t.Error("device free while its module is still running")
			}

			releaseOnce.Do(func() { close(release) })

			err := <-done
			if connect.CodeOf(err) != tt.wantCode {
				t.Fatalf("code = %v (err = %v), want %v", connect.CodeOf(err), err, tt.wantCode)
			}

			if !errors.Is(cause, tt.wantCause) {
				t.Errorf("module context cause = %v, want %v", cause, tt.wantCause)
			}

			if !heldAtStop {
				t.Error("device released before the module returned")
			}

			if hold, ok := deviceHold(svc); ok {
				t.Errorf("auto-lock still held after run returned: %+v", hold)
			}
		})
	}
}

// A user who cancels a run and retries at once is turned away while the
// cancelled run's module is still stopping: the device runs one command at a
// time, and the cancelled one still drives it. Once the module has returned,
// the retry is admitted.
func TestRunRetryWhileCancelledModuleStops(t *testing.T) {
	started, stopping, release := make(chan struct{}), make(chan struct{}), make(chan struct{})

	var (
		releaseOnce sync.Once
		retries     atomic.Int32
	)

	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	svc := newRunService(func(ctx context.Context, _ module.Session, args ...string) error {
		if fmt.Sprint(args) == "[retry]" {
			retries.Add(1)

			return nil
		}

		close(started)
		<-ctx.Done()
		close(stopping)
		<-release // still stopping, like a subprocess in its grace period

		return ctx.Err()
	})

	first := newClientStream(t, commandReq(testDevice, testCommand))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	firstDone := make(chan error, 1)

	go func() { firstDone <- svc.run(ctx, first, "alice") }()

	select {
	case <-started:
	case err := <-firstDone:
		t.Fatalf("first run returned before its module started: %v", err)
	}

	cancel()
	first.hangUp()

	select {
	case <-stopping:
	case <-time.After(time.Second):
		t.Fatal("the cancelled run's module did not see the cancellation")
	}

	// A run that returned here would have released the device under its module.
	select {
	case err := <-firstDone:
		t.Fatalf("cancelled run returned (%v) while its module was still stopping", err)
	case <-time.After(50 * time.Millisecond):
	}

	// The cancelled run's module is still stopping: the retry must be turned away.
	err := svc.run(context.Background(), newClientStream(t, commandReq(testDevice, testCommand, "retry")), "alice")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !errors.Is(err, locker.ErrAlreadyRunning) {
		t.Errorf("retry while the cancelled module stops: code = %v (err = %v), want FailedPrecondition matching ErrAlreadyRunning",
			connect.CodeOf(err), err)
	}

	if n := retries.Load(); n != 0 {
		t.Errorf("retry ran its module %d time(s) while the cancelled module still stopped", n)
	}

	releaseOnce.Do(func() { close(release) })

	if err := <-firstDone; connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("cancelled run: code = %v (err = %v), want Canceled", connect.CodeOf(err), err)
	}

	// The module has returned and the cancelled run has ended: the retry runs.
	err = svc.run(context.Background(), newClientStream(t, commandReq(testDevice, testCommand, "retry")), "alice")
	if err != nil {
		t.Fatalf("retry after the cancelled module returned: unexpected error: %v", err)
	}

	if n := retries.Load(); n != 1 {
		t.Errorf("retry ran its module %d time(s), want 1", n)
	}

	if hold, ok := deviceHold(svc); ok {
		t.Errorf("auto-lock still held after both runs returned: %+v", hold)
	}
}

// logSink collects log output written from several goroutines.
type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.Write(p)
}

func (s *logSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.String()
}

func TestWarnUntilReturned(t *testing.T) {
	newLogger := func() (*slog.Logger, *logSink) {
		sink := &logSink{}

		return slog.New(slog.NewTextHandler(sink, nil)), sink
	}

	t.Run("silent while the command is not cancelled", func(t *testing.T) {
		l, sink := newLogger()

		returned := warnUntilReturned(context.Background(), l, time.Millisecond)
		time.Sleep(20 * time.Millisecond)
		returned()

		if out := sink.String(); out != "" {
			t.Errorf("logged without a cancellation: %q", out)
		}
	})

	t.Run("silent when the module returns promptly after the cancel", func(t *testing.T) {
		l, sink := newLogger()

		ctx, cancel := context.WithCancel(context.Background())

		returned := warnUntilReturned(ctx, l, time.Hour)
		cancel()
		returned()

		if out := sink.String(); out != "" {
			t.Errorf("logged for a module that returned promptly: %q", out)
		}
	})

	t.Run("warns while a cancelled module runs on, then when it returns", func(t *testing.T) {
		l, sink := newLogger()

		ctx, cancel := context.WithCancelCause(context.Background())

		returned := warnUntilReturned(ctx, l, 5*time.Millisecond)
		cancel(errors.New("client hung up"))

		deadline := time.Now().Add(2 * time.Second)
		for !strings.Contains(sink.String(), "module still running") {
			if time.Now().After(deadline) {
				t.Fatalf("no warning while the cancelled module ran on; log: %q", sink.String())
			}

			time.Sleep(time.Millisecond)
		}

		returned()

		out := sink.String()
		if !strings.Contains(out, "client hung up") {
			t.Errorf("warning does not name the cause: %q", out)
		}

		if !strings.Contains(out, "module returned after the command was cancelled") {
			t.Errorf("no note that the module finally returned: %q", out)
		}

		time.Sleep(20 * time.Millisecond)

		if later := sink.String(); later != out {
			t.Errorf("logged after the module returned: %q", strings.TrimPrefix(later, out))
		}
	})
}
