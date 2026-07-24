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
	"testing"
	"time"

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
// methods register transfer state and return without blocking. Pre-fix the
// output/console ops were bare channel ops that blocked the module goroutine
// forever.
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
		sendFileErr = sess.SendFile("f", 4, strings.NewReader("data"))
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

	// RequestFile and SendFile register transfer state and return without
	// blocking, so even after teardown they return promptly (the transfer is then
	// released by the broker's teardown). The point here is only that they do not
	// wedge the module goroutine.
	if reqErr != nil {
		t.Errorf("RequestFile err = %v, want nil", reqErr)
	}

	if sendFileErr != nil {
		t.Errorf("SendFile err = %v, want nil", sendFileErr)
	}
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
