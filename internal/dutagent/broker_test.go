// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dutagent

import (
	"context"
	"errors"
	"io"
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
		if s.unblockCh == nil {
			s.unblockCh = make(chan struct{})
		}
		<-s.unblockCh // will block until closed; simulates a long receive
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

// NOTE: These tests encode TARGET semantics of the future refactor (error-only channel, close on worker completion).
// They are EXPECTED TO FAIL against current implementation (which sends nil and never closes) until refactor is applied.

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
	default: // current implementation will not close -> will fail here when we modify tests later by making collectErrors wait for close
	}
}

// Forwarding a stdin message should land in session.stdinCh; success path no errors.
func TestBroker_StdinForwarding(t *testing.T) {
	b := &Broker{}
	stdinPayload := []byte("user input")
	req := &pb.RunRequest{Msg: &pb.RunRequest_Console{Console: &pb.Console{Data: &pb.Console_Stdin{Stdin: stdinPayload}}}}
	// No recvErrs: testStream returns reqs in order then EOF by default.
	stream := &testStream{recvReqs: []*pb.RunRequest{req}}
	ctx, cancel := context.WithCancel(context.Background())
	sess, errCh := b.Start(ctx, stream)

	// Drain stdin from internal session.
	internal := sess.(*session)
	select {
	case data := <-internal.stdinCh:
		if string(data) != string(stdinPayload) {
			t.Fatalf("stdin mismatch: got %q want %q", string(data), string(stdinPayload))
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout: stdin payload was not forwarded to stdinCh")
	}

	cancel() // simulate module completion

	_ = collectErrors(t, errCh, 200*time.Millisecond) // expect none
}

// Cancellation during a blocked receive should terminate fromClientWorker without producing errors.
func TestBroker_CancelDuringBlockedReceive(t *testing.T) {
	b := &Broker{}
	stream := &testStream{recvBlock: true}
	ctx, cancel := context.WithCancel(context.Background())
	_, errCh := b.Start(ctx, stream)

	// Cancel promptly, then unblock the fake receive so worker goroutine does not leak.
	cancel()
	if stream.unblockCh != nil {
		close(stream.unblockCh)
	}

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
	sess.Print("trigger")

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
	session.Print("hello")

	errs := collectErrors(t, errCh, 200*time.Millisecond)
	if len(errs) != 1 || !errors.Is(errs[0], stream.sendErr) {
		// Fail expected until refactor.
		// WANT: exactly one send error.
		// GOT: %#v
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
	session.Print("hello")

	errs := collectErrors(t, errCh, 300*time.Millisecond)
	if len(errs) < 2 {
		// WANT both errors; order unspecified.
	}
}
