// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- test doubles ---------------------------------------------------------

// fakePort is an in-memory serialPort. Queued chunks are delivered by Read in
// order; once drained, Read emulates a serial read timeout (so Run stays
// responsive to context cancellation). Bytes written by the module are captured.
type fakePort struct {
	mu      sync.Mutex
	out     [][]byte
	written []byte
	closed  bool
}

type ioTimeout struct{}

func (ioTimeout) Error() string { return "i/o timeout" }

func (p *fakePort) queue(b []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.out = append(p.out, b)
}

func (p *fakePort) Read(b []byte) (int, error) {
	p.mu.Lock()
	if len(p.out) > 0 {
		chunk := p.out[0]
		n := copy(b, chunk)

		if n < len(chunk) {
			p.out[0] = chunk[n:]
		} else {
			p.out = p.out[1:]
		}

		p.mu.Unlock()

		return n, nil
	}
	p.mu.Unlock()

	// Emulate the real port's ReadTimeout so the caller's loop spins at a
	// bounded rate instead of busy-waiting.
	time.Sleep(2 * time.Millisecond)

	return 0, ioTimeout{}
}

func (p *fakePort) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.written = append(p.written, b...)

	return len(b), nil
}

func (p *fakePort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true

	return nil
}

func (p *fakePort) Flush() error { return nil }

func (p *fakePort) writtenString() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return string(p.written)
}

// syncBuffer is a goroutine-safe io.Writer for capturing client stdout, which
// the stdout pump writes concurrently with the test reading it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.Write(b)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.String()
}

// fakeSession implements module.Session for tests.
type fakeSession struct {
	stdinR  io.Reader
	stdoutW io.Writer
}

func (f *fakeSession) Print(...any)          {}
func (f *fakeSession) Printf(string, ...any) {}
func (f *fakeSession) Println(...any)        {}

func (f *fakeSession) Console() (io.Reader, io.Writer, io.Writer) {
	return f.stdinR, f.stdoutW, io.Discard
}

func (f *fakeSession) RequestFile(string) (io.Reader, error) { return nil, nil }
func (f *fakeSession) SendFile(string, io.Reader) error      { return nil }

// newSession wires a fake session with an empty stdin (the module never reads
// it) and a capturing stdout. It returns the session and the stdout capture.
func newSession(t *testing.T) (*fakeSession, *syncBuffer) {
	t.Helper()

	stdout := &syncBuffer{}

	return &fakeSession{stdinR: strings.NewReader(""), stdoutW: stdout}, stdout
}

// --- tests ----------------------------------------------------------------

func TestRunSingleExpectMatch(t *testing.T) {
	port := &fakePort{}
	port.queue([]byte("booting kernel...\n"))
	port.queue([]byte("dut login: "))

	s := &Serial{Port: "fake", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) { return port, nil }

	sess, stdout := newSession(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := s.Run(ctx, sess, "login:")
	if err != nil {
		t.Fatalf("Run returned error, want nil: %v", err)
	}

	if got := stdout.String(); !strings.Contains(got, "booting kernel") ||
		!strings.Contains(got, "Pattern matched") {
		t.Errorf("stdout missing expected content: %q", got)
	}
}

func TestRunExpectTimeout(t *testing.T) {
	port := &fakePort{}
	port.queue([]byte("nothing interesting here\n"))

	s := &Serial{Port: "fake", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) { return port, nil }

	sess, stdout := newSession(t)

	err := s.Run(context.Background(), sess, "-t", "150ms", "will-never-appear")
	if err == nil {
		t.Fatal("Run returned nil, want timeout error")
	}

	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error = %v, want timeout", err)
	}

	if got := stdout.String(); !strings.Contains(got, "Timeout reached") {
		t.Errorf("stdout missing timeout banner: %q", got)
	}
}

func TestRunExpectSendPairs(t *testing.T) {
	port := &fakePort{}
	// Both prompts are present in the output stream; both pairs must fire in
	// order, then the module drains briefly and exits.
	port.queue([]byte("dut login: "))
	port.queue([]byte("user\r\nPassword: "))

	s := &Serial{Port: "fake", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) { return port, nil }

	sess, _ := newSession(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()

	err := s.Run(ctx, sess, "login:", "admin\\n", "Password:", "secret\\n")
	if err != nil {
		t.Fatalf("Run returned error, want nil: %v", err)
	}

	if elapsed := time.Since(start); elapsed < sendDrain {
		t.Errorf("Run returned after %s, expected at least the %s drain", elapsed, sendDrain)
	}

	if got := port.writtenString(); got != "admin\nsecret\n" {
		t.Errorf("responses written to port = %q, want %q", got, "admin\nsecret\n")
	}
}

func TestRunExpectSendStopsBeforeLastResponseOnTimeout(t *testing.T) {
	port := &fakePort{}
	port.queue([]byte("dut login: ")) // only the first prompt ever appears

	s := &Serial{Port: "fake", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) { return port, nil }

	sess, _ := newSession(t)

	err := s.Run(context.Background(), sess, "-t", "200ms", "login:", "admin\\n", "Password:", "secret\\n")
	if err == nil || !strings.Contains(err.Error(), "sequence not completed") {
		t.Fatalf("Run error = %v, want 'sequence not completed' timeout", err)
	}

	// First pair fired, second did not.
	if got := port.writtenString(); got != "admin\n" {
		t.Errorf("responses written to port = %q, want %q", got, "admin\n")
	}
}

func TestRunContextCancelReturnsPromptly(t *testing.T) {
	port := &fakePort{}

	s := &Serial{Port: "fake", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) { return port, nil }

	sess, _ := newSession(t)

	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)

	go func() { runErr <- s.Run(ctx, sess) }()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-runErr:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return promptly after cancel")
	}
}

func TestRunStripsNonSGROutputButKeepsColour(t *testing.T) {
	port := &fakePort{}
	// DSR query and cursor-up (must be stripped), an SGR colour (must survive),
	// plain text on either side.
	port.queue([]byte("ABC\x1b[6nDEF\x1b[2A \x1b[31mred\x1b[0m XYZ"))

	s := &Serial{Port: "fake", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) { return port, nil }

	sess, stdout := newSession(t)

	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)

	go func() { runErr <- s.Run(ctx, sess) }()

	// Wait for the chunk to be displayed.
	deadline := time.After(2 * time.Second)

	for !strings.Contains(stdout.String(), "XYZ") {
		select {
		case <-deadline:
			t.Fatalf("output not displayed, got %q", stdout.String())
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	<-runErr

	got := stdout.String()
	if strings.Contains(got, "\x1b[6n") {
		t.Errorf("DSR query not stripped: %q", got)
	}

	if strings.Contains(got, "\x1b[2A") {
		t.Errorf("cursor-up not stripped: %q", got)
	}

	if !strings.Contains(got, "\x1b[31mred\x1b[0m") {
		t.Errorf("SGR colour not preserved: %q", got)
	}

	// The two stripped CSIs sat between ABC/DEF and after red; surrounding plain
	// text must remain intact (just with the CSIs removed).
	if !strings.Contains(got, "ABCDEF ") || !strings.Contains(got, " XYZ") {
		t.Errorf("plain text mangled: %q", got)
	}
}

// TestRunMatchSpanningReads verifies a pattern split across two serial reads is
// still matched, exercising the rolling match window.
func TestRunMatchSpanningReads(t *testing.T) {
	port := &fakePort{}
	port.queue([]byte("please lo"))
	port.queue([]byte("gin: now"))

	s := &Serial{Port: "fake", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) { return port, nil }

	sess, _ := newSession(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.Run(ctx, sess, "login:"); err != nil {
		t.Fatalf("Run returned error, want nil: %v", err)
	}
}

// TestRunSequenceLeadingSendThenExpect covers a tagged sequence that begins
// with a send (impossible with expect-send pairs): the module writes the
// leading input before any output arrives, then waits for the prompt and sends
// the reply. It ends on a send, so it drains before exiting.
func TestRunSequenceLeadingSendThenExpect(t *testing.T) {
	port := &fakePort{}
	port.queue([]byte("dut login: ")) // appears after the leading send goes out

	s := &Serial{Port: "fake", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) { return port, nil }

	sess, _ := newSession(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()

	err := s.Run(ctx, sess, `send:\n`, "expect:login:", `send:root\n`)
	if err != nil {
		t.Fatalf("Run returned error, want nil: %v", err)
	}

	if elapsed := time.Since(start); elapsed < sendDrain {
		t.Errorf("Run returned after %s, expected at least the %s drain", elapsed, sendDrain)
	}

	if got := port.writtenString(); got != "\nroot\n" {
		t.Errorf("written to port = %q, want %q", got, "\nroot\n")
	}
}

// TestRunSequenceSendOnly covers a sequence with no expect step at all: the
// module sends the input and drains briefly so the response is visible.
func TestRunSequenceSendOnly(t *testing.T) {
	port := &fakePort{}

	s := &Serial{Port: "fake", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) { return port, nil }

	sess, _ := newSession(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := s.Run(ctx, sess, `send:reboot\n`)
	if err != nil {
		t.Fatalf("Run returned error, want nil: %v", err)
	}

	if got := port.writtenString(); got != "reboot\n" {
		t.Errorf("written to port = %q, want %q", got, "reboot\n")
	}
}

// TestRunSequenceEndingOnExpectExitsWithoutDraining covers a sequence whose
// final step is an expect: the match is the completion, so the module exits
// immediately rather than draining.
func TestRunSequenceEndingOnExpectExitsWithoutDraining(t *testing.T) {
	port := &fakePort{}
	port.queue([]byte("starting\n"))
	port.queue([]byte("ready> "))

	s := &Serial{Port: "fake", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) { return port, nil }

	sess, stdout := newSession(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()

	err := s.Run(ctx, sess, `send:\n`, "expect:ready>")
	if err != nil {
		t.Fatalf("Run returned error, want nil: %v", err)
	}

	if elapsed := time.Since(start); elapsed >= sendDrain {
		t.Errorf("Run took %s; a sequence ending on expect should exit without the %s drain", elapsed, sendDrain)
	}

	if got := port.writtenString(); got != "\n" {
		t.Errorf("written to port = %q, want %q", got, "\n")
	}

	if got := stdout.String(); !strings.Contains(got, "Pattern matched") {
		t.Errorf("stdout missing match banner: %q", got)
	}
}

// TestRunDelayPausesBeforeSend verifies the configured delay paces sends: the
// leading send waits the delay before it is written to the port.
func TestRunDelayPausesBeforeSend(t *testing.T) {
	port := &fakePort{}
	port.queue([]byte("ready> ")) // appears once the leading send (after the delay) goes out

	s := &Serial{Port: "fake", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) { return port, nil }
	s.delay = 60 * time.Millisecond

	sess, _ := newSession(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()

	// Leading send is paced by the delay; the sequence ends on an expect, so it
	// exits as soon as the prompt matches (no drain to inflate the timing).
	err := s.Run(ctx, sess, `send:\n`, "expect:ready>")
	if err != nil {
		t.Fatalf("Run returned error, want nil: %v", err)
	}

	if elapsed := time.Since(start); elapsed < s.delay {
		t.Errorf("Run took %s; expected at least the %s pre-send delay", elapsed, s.delay)
	}

	if got := port.writtenString(); got != "\n" {
		t.Errorf("written to port = %q, want %q", got, "\n")
	}
}
