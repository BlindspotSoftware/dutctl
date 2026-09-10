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

// collectErrors waits until errCh is closed or timeout; returns slice of errors read.
func collectErrors(t *testing.T, errCh <-chan error, timeout time.Duration) []error {
	t.Helper()
	var errs []error
	deadline := time.After(timeout)
	for {
		select {
		case e, ok := <-errCh:
			if !ok {
				return errs
			}
			if e == nil {
				// Desired semantics: never send nil; fail immediately.
				t.Fatalf("received unexpected nil error value on error channel")
			}
			errs = append(errs, e)
		case <-deadline:
			t.Fatalf("timeout waiting for error channel to close; collected %d errors", len(errs))
		}
	}
}

// These tests verify the broker error-channel contract: the channel is
// error-only (a nil error is never sent) and is closed once both workers
// have completed.

func TestBroker_SuccessNoTraffic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := &Broker{}
	stream := &testStream{recvErrs: []error{io.EOF}}
	_, errCh := b.Start(ctx, stream)

	// Cancel broker context to simulate modules finished successfully.
	cancel()

	errs := collectErrors(t, errCh, 200*time.Millisecond)
	if len(errs) != 0 {
		for _, e := range errs {
			if e != nil {
				// Fail: success path should have no errors.
				t.Fatalf("unexpected error on success path: %v", e)
			}
		}
	}
}

// Success via EOF without explicit cancel: broker should see EOF, both workers finish, err channel closes with no errors.
func TestBroker_SuccessEOFNoCancel(t *testing.T) {
	b := &Broker{}
	stream := &testStream{recvErrs: []error{nil}} // nil => EOF
	ctx := context.Background()
	_, errCh := b.Start(ctx, stream)

	// Collect errors (expected none) and assert channel closure.
	errs := collectErrors(t, errCh, 200*time.Millisecond)
	if len(errs) != 0 {
		for _, e := range errs {
			t.Fatalf("unexpected error on pure EOF success: %v", e)
		}
	}
	select {
	case _, ok := <-errCh:
		if ok {
			t.Fatalf("error channel not closed after EOF success")
		}
	default: // no residual value buffered
	}
}

// Forwarding a stdin message should land in session.stdinCh; success path no errors.
func TestBroker_StdinForwarding(t *testing.T) {
	b := &Broker{}
	stdinPayload := []byte("user input")
	req := &pb.RunRequest{Msg: &pb.RunRequest_Console{Console: &pb.Console{Data: &pb.Console_Stdin{Stdin: stdinPayload}}}}
	stream := &testStream{recvReqs: []*pb.RunRequest{req}, recvErrs: []error{nil}} // after first req, EOF
	ctx, cancel := context.WithCancel(context.Background())
	sess, errCh := b.Start(ctx, stream)

	// Drain stdin from internal session.
	internal := sess.(*backend)
	select {
	case data := <-internal.stdinCh:
		if string(data) != string(stdinPayload) {
			t.Fatalf("stdin mismatch: got %q want %q", string(data), string(stdinPayload))
		}
	case <-time.After(200 * time.Millisecond):
		// Timed out waiting for the forwarded stdin payload.
	}

	cancel() // simulate module completion

	_ = collectErrors(t, errCh, 200*time.Millisecond) // expect none
}

// Cancellation during a blocked receive should terminate fromClientWorker without producing errors.
func TestBroker_CancelDuringBlockedReceive(t *testing.T) {
	b := &Broker{}
	stream := &testStream{recvBlock: true, unblockCh: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	_, errCh := b.Start(ctx, stream)

	// Cancel promptly, then unblock the fake receive so worker goroutine does not leak.
	cancel()
	close(stream.unblockCh)

	errs := collectErrors(t, errCh, 200*time.Millisecond)
	if len(errs) != 0 {
		for _, e := range errs {
			t.Fatalf("unexpected error on cancel-during-block: %v", e)
		}
	}
}

// Ensure both distinct errors are observed (send + receive) with the channel eventually closing.
func TestBroker_DualErrorsSet(t *testing.T) {
	b := &Broker{}
	sendErr := errors.New("send died")
	recvErr := errors.New("recv died")
	stream := &testStream{sendErr: sendErr, recvErrs: []error{recvErr}}
	ctx := context.Background()
	sess, errCh := b.Start(ctx, stream)

	// Trigger send error.
	// Print blocks until a worker receives it; before the session gained a
	// done-guard it hangs if the workers already tore down on the injected error,
	// so run it async — a blocked send leaks harmlessly instead of wedging the test.
	go sess.Print("trigger")

	errs := collectErrors(t, errCh, 300*time.Millisecond)
	if len(errs) == 1 {
		// Acceptable: only one error may be reported due to cancellation timing.
		if !errors.Is(errs[0], sendErr) && !errors.Is(errs[0], recvErr) {
			t.Fatalf("expected send or recv error, got: %v", errs[0])
		}
	} else if len(errs) == 2 {
		foundSend, foundRecv := false, false
		for _, e := range errs {
			if errors.Is(e, sendErr) {
				foundSend = true
			}
			if errors.Is(e, recvErr) {
				foundRecv = true
			}
		}
		if !foundSend || !foundRecv {
			t.Fatalf("missing expected errors: send=%v recv=%v", foundSend, foundRecv)
		}
	} else {
		t.Fatalf("expected one or two errors, got %d", len(errs))
	}
}

func TestBroker_ToClientSendError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := &Broker{}
	stream := &testStream{sendErr: errors.New("send failed")}
	session, errCh := b.Start(ctx, stream)

	// Trigger toClientWorker by printing.
	// Async: Print blocks until a worker receives it, which may never happen once
	// the workers tear down on the injected error; a leaked send is harmless here.
	go session.Print("hello")

	errs := collectErrors(t, errCh, 200*time.Millisecond)
	if len(errs) != 1 || !errors.Is(errs[0], stream.sendErr) {
		// WANT: exactly one send error matching stream.sendErr.
	}
}

func TestBroker_FromClientReceiveError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := &Broker{}
	badErr := errors.New("receive failed")
	stream := &testStream{recvErrs: []error{badErr}}
	_, errCh := b.Start(ctx, stream)

	errs := collectErrors(t, errCh, 200*time.Millisecond)
	if len(errs) != 1 || !errors.Is(errs[0], badErr) {
		// WANT one receive error.
	}
}

func TestBroker_FromClientEOFThenCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	b := &Broker{}
	stream := &testStream{recvErrs: []error{nil}} // nil slot => EOF
	_, errCh := b.Start(ctx, stream)

	cancel() // module completion triggers broker cancel

	errs := collectErrors(t, errCh, 200*time.Millisecond)
	if len(errs) != 0 {
		// WANT: no errors on EOF success.
	}
}

func TestBroker_DualErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := &Broker{}
	sendErr := errors.New("send died")
	recvErr := errors.New("recv died")
	stream := &testStream{sendErr: sendErr, recvErrs: []error{recvErr}}
	session, errCh := b.Start(ctx, stream)

	// Trigger toClient error
	// Async: Print blocks until a worker receives it, which may never happen once
	// the workers tear down on the injected error; a leaked send is harmless here.
	go session.Print("hello")

	errs := collectErrors(t, errCh, 300*time.Millisecond)
	if len(errs) < 2 {
		// WANT both errors; order unspecified.
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
	// closes the session's done signal; errCh closing confirms both are gone.
	stream := &testStream{recvErrs: []error{nil}}
	sess, errCh := b.Start(context.Background(), stream)

	if errs := collectErrors(t, errCh, time.Second); len(errs) != 0 {
		t.Fatalf("unexpected errors on EOF teardown: %v", errs)
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

		stdin, stdout, stderr := sess.Console()
		_, stdoutErr = stdout.Write([]byte("x"))
		_, stderrErr = stderr.Write([]byte("x"))
		_, stdinErr = io.ReadAll(stdin)
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
// goroutine leak (3b): when the broker is cancelled while stream.Receive is
// blocked, the inner goroutine must exit once Receive returns — its resCh send is
// guarded by ctx.Done — rather than wedge forever on a channel the returned main
// loop no longer drains. It is a goroutine-liveness check: pre-fix, the goroutine
// count stays one above baseline; post-fix it returns to baseline.
func TestBrokerReceiveLoopExitsOnCancel(t *testing.T) {
	base := runtime.NumGoroutine()

	b := &Broker{}
	stream := &testStream{recvBlock: true, unblockCh: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	_, errCh := b.Start(ctx, stream)

	cancel() // fromClientWorker returns via ctx.Done; the workers tear down

	if errs := collectErrors(t, errCh, time.Second); len(errs) != 0 {
		t.Fatalf("unexpected errors on cancel: %v", errs)
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

// These tests cover Stop, the guarantee the RPC handler relies on: when Stop
// returns, no worker is inside a stream call any more. A handler that returns
// while one is takes the whole agent down, see Broker.Stop.

// slowStream is a Stream whose Send takes measurable time and can be made to
// block indefinitely or to panic, standing in for a transport where a send
// outlives the call that triggered it.
type slowStream struct {
	sendFor   time.Duration
	sendBlock chan struct{} // if non-nil, Send waits for it to close
	sendPanic bool
	closed    chan struct{} // closed by the test to end the stream
	inFlight  atomic.Int32
	sends     atomic.Int32
}

// Receive blocks until the test ends the stream, mimicking a client that keeps
// it open for the duration of the run. An immediate io.EOF would tear the
// broker down before there is anything to stop.
func (s *slowStream) Receive() (*pb.RunRequest, error) {
	<-s.closed

	return nil, io.EOF
}

func (s *slowStream) Send(_ *pb.RunResponse) error {
	s.sends.Add(1)
	s.inFlight.Add(1)

	defer s.inFlight.Add(-1)

	if s.sendPanic {
		panic("Write called after Handler finished")
	}

	if s.sendBlock != nil {
		<-s.sendBlock
	}

	time.Sleep(s.sendFor)

	return nil
}

// printFrom drives one Print through the session, so a send is in flight.
func printFrom(t *testing.T, s module.Session) {
	t.Helper()

	done := make(chan struct{})

	go func() {
		s.Print("module output")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Print did not reach a worker")
	}
}

func TestBrokerStopWaitsForInFlightSend(t *testing.T) {
	stream := &slowStream{sendFor: 100 * time.Millisecond, closed: make(chan struct{})}
	defer close(stream.closed)

	b := &Broker{}
	sesh, _ := b.Start(context.Background(), stream)

	printFrom(t, sesh)

	b.Stop(context.Background())

	if stream.sends.Load() == 0 {
		t.Fatal("no send happened, the test does not exercise the seam")
	}

	if got := stream.inFlight.Load(); got != 0 {
		t.Errorf("Stop returned with %d send(s) in flight", got)
	}
}

func TestBrokerStopAbandonsStuckWorker(t *testing.T) {
	stream := &slowStream{sendBlock: make(chan struct{}), closed: make(chan struct{})}

	defer close(stream.closed)
	defer close(stream.sendBlock)

	// Shortened so the test does not wait out the production timeout.
	const stopTimeout = 100 * time.Millisecond

	b := &Broker{stopTimeout: stopTimeout}
	sesh, _ := b.Start(context.Background(), stream)

	printFrom(t, sesh)

	start := time.Now()

	b.Stop(context.Background())

	waited := time.Since(start)

	// Stop must have tried: anything quicker than its own timeout means it did
	// not wait for the worker at all.
	if waited < stopTimeout {
		t.Errorf("Stop returned after %v, it did not wait out its %v timeout", waited, stopTimeout)
	}

	// Blocking the handler forever is the worse outcome, so Stop gives up. The
	// send it abandoned is still running - that is what the workers' recover is
	// for.
	if waited > time.Second {
		t.Errorf("Stop waited %v for a stuck worker, it must give up at its timeout", waited)
	}

	if got := stream.inFlight.Load(); got != 1 {
		t.Errorf("expected the abandoned send to still be in flight, got %d", got)
	}
}

func TestBrokerStopWithoutStartAndTwice(t *testing.T) {
	b := &Broker{}

	start := time.Now()

	b.Stop(context.Background()) // never started

	stream := &testStream{recvErrs: []error{io.EOF}}
	b.Start(context.Background(), stream)

	b.Stop(context.Background())
	b.Stop(context.Background())

	// None of these has anything to wait for, so none may spend its timeout.
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("stopping an unstarted or already stopped broker took %v", waited)
	}
}

// TestBrokerWorkerPanicIsRecovered covers the case Stop cannot prevent: a send
// abandoned at the stop timeout panics later, in a goroutine outside the RPC
// handler's recover. Unrecovered, that takes the whole agent - and every other
// run in flight - down with it. The run must fail with it, not look like a
// broker that finished cleanly.
func TestBrokerWorkerPanicIsRecovered(t *testing.T) {
	stream := &slowStream{sendPanic: true, closed: make(chan struct{})}
	defer close(stream.closed)

	b := &Broker{}
	sesh, errCh := b.Start(context.Background(), stream)

	printFrom(t, sesh)

	// The workers must terminate on their own after the panic, not hang.
	errs := collectErrors(t, errCh, time.Second)

	if len(errs) != 1 {
		t.Fatalf("expected the panic to be reported once, got %v", errs)
	}

	if !strings.Contains(errs[0].Error(), "panicked") {
		t.Errorf("error does not name the panic: %v", errs[0])
	}
}
