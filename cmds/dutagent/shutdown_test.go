// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/locker"
	"github.com/BlindspotSoftware/dutctl/internal/rpc"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/pkg/headers"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// returnsWithin fails t unless done is closed within d.
func returnsWithin(t *testing.T, done <-chan struct{}, d time.Duration, what string) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("still waiting after %v, want it to have returned: %s", d, what)
	}
}

// stillWaiting fails t if done is closed within a few poll intervals.
func stillWaiting(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()

	select {
	case <-done:
		t.Fatalf("returned early: %s", what)
	case <-time.After(3 * pollInterval):
	}
}

// exitsAtOnce fails t unless exited receives exit1 within a second.
func exitsAtOnce(t *testing.T, exited <-chan int, what string) {
	t.Helper()

	select {
	case code := <-exited:
		if code != int(exit1) {
			t.Errorf("exit code = %d, want %d: %s", code, exit1, what)
		}
	case <-time.After(time.Second):
		t.Fatalf("did not exit: %s", what)
	}
}

// Each stop signal takes the stopper a stage further, and whoever sees a
// stage's context done finds the locker already in that stage.
func TestStopperStages(t *testing.T) {
	t.Run("stop signals take it through every stage", func(t *testing.T) {
		locks := locker.New()

		if _, err := locks.Lock("job", "alice", time.Hour); err != nil {
			t.Fatalf("setup Lock: %v", err)
		}

		sigs := make(chan os.Signal)
		exited := make(chan int, 1)

		stop := watchStops(context.Background(), sigs, locks, func(code int) { exited <- code })

		if stop.draining.Err() != nil {
			t.Fatal("draining before any stop signal")
		}

		sigs <- syscall.SIGTERM

		returnsWithin(t, stop.draining.Done(), time.Second, "the first stop signal drains")

		if stop.aborting.Err() != nil {
			t.Error("aborting after the first stop signal, want only draining")
		}

		if _, err := locks.Lock("free", "bob", time.Hour); !errors.Is(err, locker.ErrShuttingDown) {
			t.Errorf("new reservation while draining: err = %v, want ErrShuttingDown", err)
		}

		sigs <- os.Interrupt

		returnsWithin(t, stop.aborting.Done(), time.Second, "the second stop signal aborts")

		if cause := context.Cause(stop.aborting); !errors.Is(cause, errAbortedByShutdown) {
			t.Errorf("aborting cause = %v, want errAbortedByShutdown", cause)
		}

		if status := locks.StatusAll(); len(status) != 0 {
			t.Errorf("StatusAll() = %+v while aborting, want alice's reservation ended", status)
		}

		sigs <- syscall.SIGHUP

		exitsAtOnce(t, exited, "the third stop signal")
	})

	t.Run("after the agent aborted on its own the next stop signal exits", func(t *testing.T) {
		sigs := make(chan os.Signal)
		exited := make(chan int, 1)

		stop := watchStops(context.Background(), sigs, locker.New(), func(code int) { exited <- code })
		stop.abortAndWait(context.Background()) // as on a failure of the agent itself

		sigs <- syscall.SIGTERM

		exitsAtOnce(t, exited, "a stop signal in the second stage")
	})
}

// startWaitIdle runs waitIdle in the background; the returned channel is
// closed once it returns.
func startWaitIdle(locks *locker.Locker, giveUp <-chan struct{}) <-chan struct{} {
	done := make(chan struct{})

	go func() {
		waitIdle(context.Background(), locks, giveUp, "waiting")
		close(done)
	}()

	return done
}

func TestWaitIdle(t *testing.T) {
	t.Run("returns at once when nothing is held", func(t *testing.T) {
		returnsWithin(t, startWaitIdle(locker.New(), nil), time.Second, "nothing is held")
	})

	t.Run("waits until a reservation is released", func(t *testing.T) {
		locks := locker.New()

		if _, err := locks.Lock("dev", "alice", time.Hour); err != nil {
			t.Fatalf("setup Lock: %v", err)
		}

		done := startWaitIdle(locks, nil)
		stillWaiting(t, done, "the reservation is still held")

		if err := locks.ClearLock("dev", "alice"); err != nil {
			t.Fatalf("ClearLock: %v", err)
		}

		returnsWithin(t, done, time.Second, "the reservation was released")
	})

	t.Run("waits until a running command returns", func(t *testing.T) {
		locks := locker.New()

		if _, err := locks.AutoLock("dev", "alice"); err != nil {
			t.Fatalf("setup AutoLock: %v", err)
		}

		done := startWaitIdle(locks, nil)
		stillWaiting(t, done, "the command is still running")

		if err := locks.ClearAutoLock("dev", "alice"); err != nil {
			t.Fatalf("ClearAutoLock: %v", err)
		}

		returnsWithin(t, done, time.Second, "the command returned")
	})

	t.Run("returns once a reservation expires", func(t *testing.T) {
		locks := locker.New()

		if _, err := locks.Lock("dev", "alice", 2*pollInterval); err != nil {
			t.Fatalf("setup Lock: %v", err)
		}

		returnsWithin(t, startWaitIdle(locks, nil), 2*time.Second, "the reservation expired")
	})

	t.Run("returns once it gives up", func(t *testing.T) {
		locks := locker.New()

		if _, err := locks.Lock("dev", "alice", time.Hour); err != nil {
			t.Fatalf("setup Lock: %v", err)
		}

		giveUp := make(chan struct{})
		done := startWaitIdle(locks, giveUp)
		stillWaiting(t, done, "the reservation is still held")

		close(giveUp)
		returnsWithin(t, done, time.Second, "giveUp was closed")
	})
}

// startServe runs agt.serve in the background, driven by sigs; the returned
// channel receives its exit code.
func startServe(agt *agent, sigs <-chan os.Signal) (*stopper, <-chan exitCode) {
	stop := watchStops(context.Background(), sigs, agt.locks, func(int) {})
	code := make(chan exitCode, 1)

	go func() { code <- agt.serve(context.Background(), stop) }()

	return stop, code
}

func newServeAgent() *agent {
	return &agent{address: "127.0.0.1:0", locks: locker.New()}
}

// serveReturns fails t unless serve has returned want within d.
func serveReturns(t *testing.T, code <-chan exitCode, d time.Duration, want exitCode, what string) {
	t.Helper()

	select {
	case got := <-code:
		if got != want {
			t.Errorf("serve returned %d, want %d: %s", got, want, what)
		}
	case <-time.After(d):
		t.Fatalf("serve still running after %v: %s", d, what)
	}
}

// serveStillRunning fails t if serve returns within a few poll intervals.
func serveStillRunning(t *testing.T, code <-chan exitCode, what string) {
	t.Helper()

	select {
	case got := <-code:
		t.Fatalf("serve returned %d early: %s", got, what)
	case <-time.After(3 * pollInterval):
	}
}

func TestServeStopStages(t *testing.T) {
	t.Run("an idle agent stops at once", func(t *testing.T) {
		sigs := make(chan os.Signal, 1)
		_, code := startServe(newServeAgent(), sigs)

		sigs <- syscall.SIGTERM

		serveReturns(t, code, 2*time.Second, exit0, "nothing runs and nothing is reserved")
	})

	t.Run("stage 1 waits until the reservations have ended", func(t *testing.T) {
		agt := newServeAgent()

		if _, err := agt.locks.Lock("dev", "alice", time.Hour); err != nil {
			t.Fatalf("setup Lock: %v", err)
		}

		sigs := make(chan os.Signal, 1)
		_, code := startServe(agt, sigs)

		sigs <- syscall.SIGTERM

		serveStillRunning(t, code, "alice's reservation is held")

		if _, err := agt.locks.AutoLock("free", "bob"); !errors.Is(err, locker.ErrShuttingDown) {
			t.Errorf("bob's command on a free device while draining: err = %v, want ErrShuttingDown", err)
		}

		// The job goes on while the agent drains: alice runs her next command.
		if _, err := agt.locks.AutoLock("dev", "alice"); err != nil {
			t.Fatalf("alice's command while draining: %v", err)
		}

		if err := agt.locks.ClearAutoLock("dev", "alice"); err != nil {
			t.Fatalf("ClearAutoLock: %v", err)
		}

		if err := agt.locks.ClearLock("dev", "alice"); err != nil {
			t.Fatalf("ClearLock: %v", err)
		}

		serveReturns(t, code, 2*time.Second, exit0, "alice's reservation was released")
	})

	t.Run("stage 2 ends the reservations and waits for the running commands", func(t *testing.T) {
		agt := newServeAgent()

		if _, err := agt.locks.Lock("job", "alice", time.Hour); err != nil {
			t.Fatalf("setup Lock: %v", err)
		}

		if _, err := agt.locks.AutoLock("busy", "bob"); err != nil {
			t.Fatalf("setup AutoLock: %v", err)
		}

		sigs := make(chan os.Signal, 1)
		stop, code := startServe(agt, sigs)

		sigs <- syscall.SIGTERM
		sigs <- syscall.SIGTERM

		returnsWithin(t, stop.aborting.Done(), time.Second, "the second stop signal aborts")

		serveStillRunning(t, code, "bob's command is still running")

		if status := agt.locks.StatusAll(); len(status) != 1 || status["busy"].Owner != "bob" {
			t.Errorf("StatusAll() = %+v in stage 2, want only bob's running command", status)
		}

		if err := agt.locks.ClearAutoLock("busy", "bob"); err != nil {
			t.Fatalf("ClearAutoLock: %v", err)
		}

		serveReturns(t, code, 2*time.Second, exit0, "bob's command returned")
	})

	// A failing service enters stage 2 like a second stop signal: it ends what
	// is reserved, takes no new work and waits for the running commands, so no
	// module is deinitialized while it runs.
	t.Run("a failing service aborts and waits for the running commands", func(t *testing.T) {
		agt := newServeAgent()
		agt.address = "no-port" // the service cannot listen

		if _, err := agt.locks.AutoLock("busy", "bob"); err != nil {
			t.Fatalf("setup AutoLock: %v", err)
		}

		stop, code := startServe(agt, nil)

		returnsWithin(t, stop.aborting.Done(), time.Second, "the service failure aborts")

		serveStillRunning(t, code, "bob's command is still running")

		if _, err := agt.locks.AutoLock("free", "carol"); !errors.Is(err, locker.ErrShuttingDown) {
			t.Errorf("new command after a service failure: err = %v, want ErrShuttingDown", err)
		}

		if err := agt.locks.ClearAutoLock("busy", "bob"); err != nil {
			t.Fatalf("ClearAutoLock: %v", err)
		}

		serveReturns(t, code, 2*time.Second, exit1, "bob's command returned after the service failed")
	})
}

// The real stop signals count together: SIGHUP, SIGINT and SIGTERM, here sent
// to the test process itself, each take the agent a stage further. Using all
// three proves that each one is registered.
func TestNotifyStopsCountsStopSignals(t *testing.T) {
	for _, sig := range []os.Signal{syscall.SIGHUP, os.Interrupt} {
		if signal.Ignored(sig) {
			t.Skipf("%v is ignored in this process, so notifyStops leaves it alone by design", sig)
		}
	}

	exited := make(chan int, 1)

	stop, unnotify := notifyStops(context.Background(), locker.New(), func(code int) { exited <- code })
	defer unnotify()

	self := os.Getpid()

	if err := syscall.Kill(self, syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}

	returnsWithin(t, stop.draining.Done(), 2*time.Second, "SIGHUP is the first stop signal")

	if err := syscall.Kill(self, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}

	returnsWithin(t, stop.aborting.Done(), 2*time.Second, "SIGINT is the second stop signal")

	if err := syscall.Kill(self, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	exitsAtOnce(t, exited, "SIGTERM, the third stop signal")
}

func TestDescribeHolds(t *testing.T) {
	until := time.Date(2026, 1, 2, 14, 30, 0, 0, time.Local)

	got := describeHolds(map[string]locker.Hold{
		"dev2": {Kind: locker.Reserved, Owner: "bob", ExpiresAt: until},
		"dev1": {Kind: locker.Busy, Owner: "alice"},
	})

	want := "dev1 (running a command for alice), dev2 (reserved by bob until 14:30)"
	if got != want {
		t.Errorf("describeHolds = %q, want %q", got, want)
	}

	if got := describeHolds(nil); got != "" {
		t.Errorf("describeHolds(nil) = %q, want \"\"", got)
	}
}

// freeAddr returns a local address that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	addr := ln.Addr().String()
	ln.Close()

	return addr
}

// runOver runs the test command on device for user through client; the
// returned channel receives the run's final error, nil on success.
func runOver(client dutctlv1connect.DeviceServiceClient, user, device string) <-chan error {
	res := make(chan error, 1)

	go func() {
		stream := client.Run(context.Background())
		stream.RequestHeader().Set(headers.User, user)

		err := stream.Send(commandReq(device, testCommand))
		if err != nil && !errors.Is(err, io.EOF) {
			res <- err

			return
		}

		for {
			_, err := stream.Receive()
			if errors.Is(err, io.EOF) {
				res <- nil

				return
			}

			if err != nil {
				res <- err

				return
			}
		}
	}()

	return res
}

// End to end through the real RPC service: in stage 1 a job that holds a
// reservation goes on while new work is refused, and the second stop signal
// aborts the job's running command with Aborted "dutagent is shutting down";
// serve returns only once its module has returned.
func TestServeAbortsRunAdmittedWhileDraining(t *testing.T) {
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.DiscardHandler))

	started, returned := make(chan struct{}), make(chan struct{})

	blocking := funcModule(func(ctx context.Context, _ module.Session, _ ...string) error {
		close(started)
		<-ctx.Done()
		time.Sleep(3 * pollInterval) // still stopping, like a subprocess in its grace period
		close(returned)

		return ctx.Err()
	})
	quick := funcModule(func(context.Context, module.Session, ...string) error { return nil })

	device := func(mod funcModule) dut.Device {
		m := dut.Module{Module: mod}
		m.Config.Name = "mod0"
		m.Config.Passthrough = true

		return dut.Device{Cmds: map[string]dut.Command{testCommand: {Modules: []dut.Module{m}}}}
	}

	agt := &agent{address: freeAddr(t), locks: locker.New()}
	agt.config.Devices = dut.Devlist{"job": device(blocking), "free": device(quick)}

	if _, err := agt.locks.Lock("job", "alice", time.Hour); err != nil {
		t.Fatalf("setup Lock: %v", err)
	}

	sigs := make(chan os.Signal, 1)
	stop, code := startServe(agt, sigs)

	client := rpc.NewDeviceClient(agt.address)

	// Wait until the service answers.
	for deadline := time.Now().Add(5 * time.Second); ; {
		_, err := client.List(context.Background(), connect.NewRequest(&pb.ListRequest{}))
		if err == nil {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("rpc service not ready: %v", err)
		}

		time.Sleep(10 * time.Millisecond)
	}

	sigs <- syscall.SIGTERM

	// The stopper drains the locker before it ends the draining stage.
	returnsWithin(t, stop.draining.Done(), time.Second, "the first stop signal drains")

	// Over the wire, only the message tells which refusal it is.
	err := <-runOver(client, "bob", "free")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), locker.ErrShuttingDown.Error()) {
		t.Fatalf("bob's command in stage 1: err = %v, want FailedPrecondition %q", err, locker.ErrShuttingDown)
	}

	alices := runOver(client, "alice", "job")

	select {
	case <-started:
	case err := <-alices:
		t.Fatalf("alice's command on her reserved device in stage 1 was not admitted: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("alice's module did not start")
	}

	sigs <- syscall.SIGTERM

	select {
	case err := <-alices:
		if connect.CodeOf(err) != connect.CodeAborted || !strings.Contains(err.Error(), errAbortedByShutdown.Error()) {
			t.Errorf("alice's run: err = %v, want Aborted %q", err, errAbortedByShutdown)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("alice's run was not aborted by the second stop signal")
	}

	select {
	case got := <-code:
		select {
		case <-returned:
		default:
			t.Fatalf("serve returned %d before alice's module returned", got)
		}

		if got != exit0 {
			t.Errorf("serve returned %d, want %d", got, exit0)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after the aborted module returned")
	}
}
