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

	// disconnectErr, if set, is returned once by Read after all queued output is
	// drained, simulating the device disappearing. Subsequent reads time out.
	disconnectErr error
	disconnected  bool
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

	if p.disconnectErr != nil && !p.disconnected {
		p.disconnected = true
		err := p.disconnectErr
		p.mu.Unlock()

		return 0, err
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

// newSession wires a fake session with a pipe-backed stdin (so the stdin pump
// unblocks on cleanup) and a capturing stdout. It returns the session, the
// stdout capture, and the stdin write end.
func newSession(t *testing.T) (*fakeSession, *syncBuffer, *io.PipeWriter) {
	t.Helper()

	pr, pw := io.Pipe()
	stdout := &syncBuffer{}

	t.Cleanup(func() { _ = pw.Close() }) // EOF unblocks the stdin pump

	return &fakeSession{stdinR: pr, stdoutW: stdout}, stdout, pw
}

// --- tests ----------------------------------------------------------------

func TestRunSingleExpectMatch(t *testing.T) {
	port := &fakePort{}
	port.queue([]byte("booting kernel...\n"))
	port.queue([]byte("dut login: "))

	s := &Serial{Port: "fake", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) { return port, nil }

	sess, stdout, _ := newSession(t)

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

	sess, stdout, _ := newSession(t)

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

	sess, _, _ := newSession(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()

	err := s.Run(ctx, sess, "login:", "admin\\n", "Password:", "secret\\n")
	if err != nil {
		t.Fatalf("Run returned error, want nil: %v", err)
	}

	if elapsed := time.Since(start); elapsed < pairsDrain {
		t.Errorf("Run returned after %s, expected at least the %s drain", elapsed, pairsDrain)
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

	sess, _, _ := newSession(t)

	err := s.Run(context.Background(), sess, "-t", "200ms", "login:", "admin\\n", "Password:", "secret\\n")
	if err == nil || !strings.Contains(err.Error(), "sequence not completed") {
		t.Fatalf("Run error = %v, want 'sequence not completed' timeout", err)
	}

	// First pair fired, second did not.
	if got := port.writtenString(); got != "admin\n" {
		t.Errorf("responses written to port = %q, want %q", got, "admin\n")
	}
}

func TestRunInteractiveForwardsStdinToPort(t *testing.T) {
	port := &fakePort{}

	s := &Serial{Port: "fake", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) { return port, nil }
	// interactive: no args

	sess, _, stdin := newSession(t)

	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)

	go func() { runErr <- s.Run(ctx, sess) }()

	if _, err := stdin.Write([]byte("reboot\n")); err != nil {
		t.Fatalf("write stdin: %v", err)
	}

	// Wait until the keystrokes reach the port.
	deadline := time.After(2 * time.Second)

	for port.writtenString() != "reboot\n" {
		select {
		case <-deadline:
			t.Fatalf("port did not receive stdin, got %q", port.writtenString())
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()

	select {
	case err := <-runErr:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunContextCancelReturnsPromptly(t *testing.T) {
	port := &fakePort{}

	s := &Serial{Port: "fake", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) { return port, nil }

	sess, _, _ := newSession(t)

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

	sess, stdout, _ := newSession(t)

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

	sess, _, _ := newSession(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.Run(ctx, sess, "login:"); err != nil {
		t.Fatalf("Run returned error, want nil: %v", err)
	}
}

type deviceGoneError struct{}

func (deviceGoneError) Error() string { return "read /dev/ttyUSB0: input/output error" }

// TestRunReconnectsOnDeviceLoss verifies the module survives the serial device
// disappearing mid-session, reopens it once it reappears, and keeps matching.
func TestRunReconnectsOnDeviceLoss(t *testing.T) {
	// First port delivers some output then reports a disconnect.
	port1 := &fakePort{disconnectErr: deviceGoneError{}}
	port1.queue([]byte("booting...\n"))

	// Second port (the reappeared device) presents the prompt to match.
	port2 := &fakePort{}
	port2.queue([]byte("dut login: "))

	dialed := 0

	s := &Serial{Port: "/dev/ttyUSB0", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) {
		dialed++
		if dialed == 1 {
			return port1, nil
		}

		return port2, nil
	}

	sess, stdout, _ := newSession(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := s.Run(ctx, sess, "login:"); err != nil {
		t.Fatalf("Run returned error, want nil: %v", err)
	}

	if dialed < 2 {
		t.Errorf("dial called %d times, want >= 2 (reconnect)", dialed)
	}

	got := stdout.String()
	if !strings.Contains(got, "disconnected") || !strings.Contains(got, "reconnected") {
		t.Errorf("missing reconnect status messages: %q", got)
	}

	if !strings.Contains(got, "Pattern matched") {
		t.Errorf("pattern not matched after reconnect: %q", got)
	}
}

// TestRunReconnectAbortsOnTimeout verifies the expect timeout still fires while
// the module is waiting for a missing device to come back.
func TestRunReconnectAbortsOnTimeout(t *testing.T) {
	port := &fakePort{disconnectErr: deviceGoneError{}}
	port.queue([]byte("hello\n"))

	s := &Serial{Port: "/dev/ttyUSB0", Baud: DefaultBaudRate}
	s.dialPort = func() (serialPort, error) {
		// Device never comes back after the first open.
		if port.disconnected {
			return nil, deviceGoneError{}
		}

		return port, nil
	}

	sess, _, _ := newSession(t)

	err := s.Run(context.Background(), sess, "-t", "300ms", "will-never-appear")
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("Run error = %v, want timeout while reconnecting", err)
	}
}

// TestRunReconnectsWhenDeviceNodeVanishes covers the device-loss path that real
// hardware actually hits: a removed USB serial adapter surfaces to Read as an
// idle io.EOF/timeout (NOT a distinct "device gone" error), so the module must
// notice the vanished device node and reconnect instead of spinning forever on
// what looks like a benign idle read.
func TestRunReconnectsWhenDeviceNodeVanishes(t *testing.T) {
	port := &fakePort{} // no queued data: idle reads return a timeout, like a quiet port

	opens := 0

	s := &Serial{Port: "/dev/fake", Baud: DefaultBaudRate}
	s.deviceLossGrace = 20 * time.Millisecond    // suspect loss quickly in the test
	s.portPresent = func() bool { return false } // the device node has vanished
	s.dialPort = func() (serialPort, error) {
		opens++
		if opens == 1 {
			return port, nil // initial open succeeds
		}

		return nil, deviceGoneError{} // device stays gone while reconnecting
	}

	sess, stdout, _ := newSession(t)

	err := s.Run(context.Background(), sess, "-t", "300ms", "will-never-appear")
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("Run error = %v, want timeout", err)
	}

	if got := stdout.String(); !strings.Contains(got, "disconnected, waiting to reconnect") {
		t.Errorf("expected reconnect to start after the device node vanished on idle EOF, got %q", got)
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

	sess, _, _ := newSession(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()

	err := s.Run(ctx, sess, `send:\n`, "expect:login:", `send:root\n`)
	if err != nil {
		t.Fatalf("Run returned error, want nil: %v", err)
	}

	if elapsed := time.Since(start); elapsed < pairsDrain {
		t.Errorf("Run returned after %s, expected at least the %s drain", elapsed, pairsDrain)
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

	sess, _, _ := newSession(t)

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

	sess, stdout, _ := newSession(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()

	err := s.Run(ctx, sess, `send:\n`, "expect:ready>")
	if err != nil {
		t.Fatalf("Run returned error, want nil: %v", err)
	}

	if elapsed := time.Since(start); elapsed >= pairsDrain {
		t.Errorf("Run took %s; a sequence ending on expect should exit without the %s drain", elapsed, pairsDrain)
	}

	if got := port.writtenString(); got != "\n" {
		t.Errorf("written to port = %q, want %q", got, "\n")
	}

	if got := stdout.String(); !strings.Contains(got, "Pattern matched") {
		t.Errorf("stdout missing match banner: %q", got)
	}
}
