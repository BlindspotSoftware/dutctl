// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// testStream is a controllable fake implementing Stream.
// It allows injection of send/receive errors and scripted receive results.
// Concurrency notes: minimal locking because tests serialize access.
type testStream struct {
	sendErr   error
	recvErrs  []error          // legacy error scripting (nil => EOF)
	recvReqs  []*pb.RunRequest // scripted requests (paired with nil error)
	recvBlock bool             // if true, Receive blocks until unblockCh is closed
	unblockCh chan struct{}    // used when recvBlock is set
	recvCalls int
}

func (s *testStream) Send(_ *pb.RunResponse) error {
	return s.sendErr
}

func (s *testStream) Receive() (*pb.RunRequest, error) {
	if s.recvBlock {
		<-s.unblockCh // blocks until the test closes it; simulates a long receive
	}

	idx := s.recvCalls
	s.recvCalls++

	// Prioritize explicit error scripting.
	if idx < len(s.recvErrs) {
		err := s.recvErrs[idx]
		if err == nil {
			return nil, io.EOF
		}
		return nil, err
	}

	if idx < len(s.recvReqs) {
		return s.recvReqs[idx], nil
	}

	return nil, io.EOF
}

// blockingStream returns a testStream whose Receive blocks, as a client holding
// the stream open does, until the test ends; its Send fails with sendErr if set.
func blockingStream(t *testing.T, sendErr error) *testStream {
	t.Helper()

	s := &testStream{sendErr: sendErr, recvBlock: true, unblockCh: make(chan struct{})}
	t.Cleanup(func() { close(s.unblockCh) })

	return s
}

// start starts b over stream, returning the session and the modules' context,
// and stops b when the test ends.
func start(t *testing.T, b *Broker, stream Stream) (module.Session, context.Context) {
	t.Helper()

	sess, runCtx := b.Start(context.Background(), stream)
	t.Cleanup(func() { _ = b.Stop() })

	return sess, runCtx
}

// awaitFailure waits until the broker has reported a worker failure.
func awaitFailure(t *testing.T, runCtx context.Context) {
	t.Helper()

	select {
	case <-runCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("no worker failure reported")
	}
}

// awaitClosed waits until the session has been torn down, its workers gone.
func awaitClosed(t *testing.T, sess module.Session) {
	t.Helper()

	select {
	case <-sess.(*backend).done:
	case <-time.After(time.Second):
		t.Fatal("session not closed")
	}
}

func TestBrokerStopWithoutFailure(t *testing.T) {
	b := &Broker{}
	_, runCtx := start(t, b, blockingStream(t, nil))

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: unexpected error: %v", err)
	}

	// Stop ends the modules' context, but not with a failure.
	if cause := context.Cause(runCtx); !errors.Is(cause, context.Canceled) {
		t.Errorf("run context cause after a clean stop = %v, want context.Canceled", cause)
	}
}

// A client that closes its side ends the workers, but not the modules: they may
// still be running and must not be aborted.
func TestBrokerClientCloseLeavesModulesRunning(t *testing.T) {
	b := &Broker{}
	sess, runCtx := start(t, b, &testStream{recvErrs: []error{nil}}) // nil => EOF

	awaitClosed(t, sess)

	if err := runCtx.Err(); err != nil {
		t.Errorf("run context done after the client closed its side: %v (cause %v)", err, context.Cause(runCtx))
	}

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: unexpected error: %v", err)
	}
}

// Cancelling the context passed to Start tears the session down by itself, so a
// module blocked in a session call unwinds even before anyone calls Stop.
func TestBrokerParentCancelClosesSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	b := &Broker{}
	sess, runCtx := b.Start(ctx, blockingStream(t, nil))

	cancel()

	awaitClosed(t, sess)

	if runCtx.Err() == nil {
		t.Error("modules' context still live after its parent was cancelled")
	}

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: unexpected error: %v", err)
	}
}

// Input the client sends while no console is open is dropped, not queued: the
// stream ends right after it, and the broker stops cleanly rather than parking
// its upstream worker on input nobody reads.
func TestBrokerInputWithoutConsoleDropped(t *testing.T) {
	b := &Broker{}
	req := &pb.RunRequest{Msg: &pb.RunRequest_ConsoleInput{ConsoleInput: &pb.ConsoleInput{Data: []byte("user input")}}}
	stream := &testStream{recvReqs: []*pb.RunRequest{req}} // EOF after the request
	sess, _ := start(t, b, stream)

	awaitClosed(t, sess)

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: unexpected error: %v", err)
	}
}

// Cancellation during a blocked receive should terminate fromClientWorker without producing errors.
func TestBrokerCancelDuringBlockedReceive(t *testing.T) {
	b := &Broker{}
	stream := &testStream{recvBlock: true, unblockCh: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	b.Start(ctx, stream)

	// Cancel promptly, then unblock the fake receive so worker goroutine does not leak.
	cancel()
	close(stream.unblockCh)

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: unexpected error on cancel-during-block: %v", err)
	}
}

func TestBrokerWorkerFailure(t *testing.T) {
	sendErr := errors.New("send failed")
	recvErr := errors.New("receive failed")

	tests := []struct {
		name    string
		stream  func(t *testing.T) *testStream
		print   bool // print once, to make the downstream worker send
		wantErr error
	}{
		{
			name:    "send fails",
			stream:  func(t *testing.T) *testStream { t.Helper(); return blockingStream(t, sendErr) },
			print:   true,
			wantErr: sendErr,
		},
		{
			name:    "receive fails",
			stream:  func(*testing.T) *testStream { return &testStream{recvErrs: []error{recvErr}} },
			wantErr: recvErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Broker{}
			sess, runCtx := start(t, b, tt.stream(t))

			if tt.print {
				// Async: after the failure the print is dropped via the done
				// signal, but it must not hold up the test either way.
				go sess.Print("hello")
			}

			awaitFailure(t, runCtx)

			if cause := context.Cause(runCtx); !errors.Is(cause, tt.wantErr) {
				t.Errorf("run context cause = %v, want %v", cause, tt.wantErr)
			}

			if err := b.Stop(); !errors.Is(err, tt.wantErr) {
				t.Errorf("Stop = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// When both workers fail, Stop and the run context's cause must name the same
// failure: the first one.
func TestBrokerFirstFailureWins(t *testing.T) {
	b := &Broker{}
	stream := &testStream{sendErr: errors.New("send died"), recvErrs: []error{errors.New("recv died")}}
	sess, runCtx := start(t, b, stream)

	go sess.Print("hello")

	awaitFailure(t, runCtx)

	err := b.Stop()
	if err == nil {
		t.Fatal("Stop = nil, want the first worker failure")
	}

	if cause := context.Cause(runCtx); !errors.Is(cause, err) {
		t.Errorf("run context cause = %v, but Stop reports %v", cause, err)
	}
}

// TestBrokerSessionCallsUnblockAfterTeardown is a regression test for the
// module-goroutine leak (3a): once the broker's workers have exited, every
// module-facing session call must unblock via the frozen done signal instead of
// wedging on a channel whose worker peer is gone. Output methods drop; the
// Console reader reports io.EOF and the writers io.ErrClosedPipe; the file
// methods return an error. Pre-fix these were bare channel ops that blocked the
// module goroutine forever.
func TestBrokerSessionCallsUnblockAfterTeardown(t *testing.T) {
	b := &Broker{}
	// Immediate EOF makes fromClientWorker return, which cancels the workers and
	// closes the session's done signal; Stop returning confirms both are gone.
	stream := &testStream{recvErrs: []error{nil}}
	sess, _ := start(t, b, stream)

	if err := b.Stop(); err != nil {
		t.Fatalf("unexpected error on EOF teardown: %v", err)
	}

	finished := make(chan struct{})

	var (
		stdoutErr, stderrErr, stdinErr, reqErr, sendFileErr error
	)

	go func() {
		defer close(finished)

		// None of these must block now that the workers are gone.
		sess.Print("dropped")
		sess.Printf("%s", "dropped")
		sess.Println("dropped")

		con := sess.OpenConsole(module.ConsoleOptions{Mode: module.ConsoleRaw})
		_, stdoutErr = con.Stdout.Write([]byte("x"))
		_, stderrErr = con.Stderr.Write([]byte("x"))
		_, stdinErr = io.ReadAll(con.Stdin)
		_, reqErr = sess.RequestFile("f")
		sendFileErr = sess.SendFile("f", strings.NewReader("data"))
	}()

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("a session call wedged after the workers exited (module goroutine would leak)")
	}

	if !errors.Is(stdoutErr, io.ErrClosedPipe) {
		t.Errorf("stdout.Write err = %v, want io.ErrClosedPipe", stdoutErr)
	}

	if !errors.Is(stderrErr, io.ErrClosedPipe) {
		t.Errorf("stderr.Write err = %v, want io.ErrClosedPipe", stderrErr)
	}

	if stdinErr != nil {
		t.Errorf("stdin io.ReadAll err = %v, want nil (EOF terminates ReadAll)", stdinErr)
	}

	if !errors.Is(reqErr, errSessionClosed) {
		t.Errorf("RequestFile err = %v, want errSessionClosed", reqErr)
	}

	if !errors.Is(sendFileErr, errSessionClosed) {
		t.Errorf("SendFile err = %v, want errSessionClosed", sendFileErr)
	}
}

// TestBackendCurrentFileRace guards the mutex on currentFile: it is read and
// written from three goroutines (SendFile on the module goroutine, and both
// broker workers) with no channel handing it between them. Concurrent access
// without the lock is a data race; run under -race this fails if the guarding
// mutex is dropped.
func TestBackendCurrentFileRace(t *testing.T) {
	b := &backend{}

	var wg sync.WaitGroup

	for range 50 {
		wg.Add(2)

		go func() { defer wg.Done(); b.setCurrentFile("image.bin") }()
		go func() { defer wg.Done(); _ = b.currentFileName() }()
	}

	wg.Wait()
}

// TestBrokerReceiveLoopExitsOnCancel is a regression test for the receive-loop
// goroutine leak (3b): when the broker is stopped while stream.Receive is
// blocked, the inner goroutine must exit once Receive returns — its resCh send is
// guarded by ctx.Done — rather than wedge forever on a channel the returned main
// loop no longer drains. It is a goroutine-liveness check: pre-fix, the goroutine
// count stays one above baseline; post-fix it returns to baseline.
func TestBrokerReceiveLoopExitsOnCancel(t *testing.T) {
	base := runtime.NumGoroutine()

	b := &Broker{}
	stream := &testStream{recvBlock: true, unblockCh: make(chan struct{})}
	b.Start(context.Background(), stream)

	// fromClientWorker returns via ctx.Done; the workers tear down.
	if err := b.Stop(); err != nil {
		t.Fatalf("unexpected error on stop: %v", err)
	}

	// The inner receive-loop goroutine is still parked in the fake's blocking
	// Receive. Releasing it must let it exit (its guarded send sees ctx.Done).
	close(stream.unblockCh)

	deadline := time.After(2 * time.Second)
	for {
		if runtime.NumGoroutine() <= base {
			return // the receive-loop goroutine exited: no leak
		}

		select {
		case <-deadline:
			t.Fatalf("receive-loop goroutine did not exit: goroutines=%d baseline=%d",
				runtime.NumGoroutine(), base)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// slowStream is a Stream whose Send takes measurable time, standing in for a
// transport where a send outlives the call that triggered it. It records how many
// sends are in flight.
type slowStream struct {
	sendFor  time.Duration
	closed   chan struct{} // closed by the test to end the stream
	inFlight atomic.Int32
	sends    atomic.Int32
}

// Receive blocks until the test ends the stream, mimicking a client that keeps
// it open for the duration of the run. An immediate io.EOF would tear the
// broker down before there is anything to wait for.
func (s *slowStream) Receive() (*pb.RunRequest, error) {
	<-s.closed

	return nil, io.EOF
}

func (s *slowStream) Send(_ *pb.RunResponse) error {
	s.sends.Add(1)
	s.inFlight.Add(1)

	defer s.inFlight.Add(-1)

	time.Sleep(s.sendFor)

	return nil
}

// TestBrokerStopWaitsForInFlightSend covers the guarantee the RPC handler relies
// on: once Stop returned, no Send is in flight.
func TestBrokerStopWaitsForInFlightSend(t *testing.T) {
	stream := &slowStream{sendFor: 100 * time.Millisecond, closed: make(chan struct{})}
	defer close(stream.closed)

	b := &Broker{}
	sesh, _ := start(t, b, stream)

	// Print returns once a worker has taken the message, while its Send is
	// still running.
	sesh.Print("module output")

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: unexpected error: %v", err)
	}

	// The worker took the message, so it must have sent it by the time Stop
	// returns: a send not yet started or still running both outlive Stop.
	if sends, inFlight := stream.sends.Load(), stream.inFlight.Load(); sends != 1 || inFlight != 0 {
		t.Errorf("a send outlived Stop (sends=%d, in flight=%d)", sends, inFlight)
	}
}

func TestBrokerStopWithoutStartAndTwice(t *testing.T) {
	done := make(chan struct{})

	go func() {
		defer close(done)

		b := &Broker{}
		if err := b.Stop(); err != nil { // never started
			t.Errorf("Stop on an unstarted broker = %v, want nil", err)
		}

		b.Start(context.Background(), &testStream{recvErrs: []error{io.EOF}})

		_ = b.Stop()
		_ = b.Stop()
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop blocked on an unstarted or already stopped broker")
	}
}
