// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
)

// errDeviceGone models the hard read error a removed USB serial adapter surfaces.
var errDeviceGone = errors.New("read /dev/ttyUSB0: input/output error")

// livePort is a goroutine-safe port fake for the interactive tests, where the
// stdin pump writes to the port concurrently with the read loop and the test
// polling written bytes. Queued chunks are delivered by Read in order; once
// drained it optionally reports a one-shot disconnect, then emulates a serial
// read timeout so the read loop spins at a bounded rate.
type livePort struct {
	mu            sync.Mutex
	out           [][]byte
	written       []byte
	closed        bool
	disconnectErr error
	disconnected  bool
}

func (p *livePort) queue(b []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.out = append(p.out, b)
}

func (p *livePort) Read(b []byte) (int, error) {
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

	// Emulate the real port's read timeout so the read loop is bounded, not a
	// busy-wait. The lock is released first so a concurrent write never blocks.
	time.Sleep(2 * time.Millisecond)

	return 0, nil
}

func (p *livePort) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.written = append(p.written, b...)

	return len(b), nil
}

func (p *livePort) ResetInputBuffer() error { return nil }

func (p *livePort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true

	return nil
}

func (p *livePort) writtenString() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return string(p.written)
}

// syncBuffer is a goroutine-safe io.Writer for capturing interactive stdout,
// which the read loop writes concurrently with the test reading it.
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

// interactiveSession wires a mock.Session with a pipe-backed stdin (so the stdin
// pump unblocks on cleanup) and a capturing stdout. It returns the session, the
// stdout capture, and the stdin write end.
func interactiveSession(t *testing.T) (*mock.Session, *syncBuffer, *io.PipeWriter) {
	t.Helper()

	pr, pw := io.Pipe()
	stdout := &syncBuffer{}

	t.Cleanup(func() { _ = pw.Close() }) // EOF unblocks the stdin pump

	return &mock.Session{Stdin: pr, Stdout: stdout, Stderr: io.Discard}, stdout, pw
}

// checkMarkersOnce fails the test unless out reports the device loss and the
// reconnect exactly once each.
func checkMarkersOnce(t *testing.T, out string) {
	t.Helper()

	for _, marker := range []string{"Serial device disconnected", "Serial device reconnected"} {
		if n := strings.Count(out, marker); n != 1 {
			t.Errorf("%q appears %d times, want once: %q", marker, n, out)
		}
	}
}

// waitFor polls cond until it holds or a short deadline elapses.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.After(2 * time.Second)

	for !cond() {
		select {
		case <-deadline:
			t.Fatal("condition not met within deadline")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestRunInteractiveForwardsStdinToPort(t *testing.T) {
	p := &livePort{}

	s := &Serial{Port: "/dev/fake", Baud: DefaultBaudRate}
	s.open = func(_ string, _ int) (port, error) { return p, nil }

	sess, _, stdin := interactiveSession(t)

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)

	go func() { runErr <- s.Run(ctx, sess, "-i") }()

	if _, err := stdin.Write([]byte("reboot\n")); err != nil {
		t.Fatalf("write stdin: %v", err)
	}

	waitFor(t, func() bool { return p.writtenString() == "reboot\n" })

	cancel()

	select {
	case err := <-runErr:
		// Cancellation is the normal end of an interactive session (client
		// disconnect / quit); see runInteractive.
		if err != nil {
			t.Errorf("Run error = %v, want nil on clean end", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// TestRunInteractiveReconnectsOnDeviceLoss verifies an interactive session
// survives the serial device disappearing, reopens it, and keeps streaming.
func TestRunInteractiveReconnectsOnDeviceLoss(t *testing.T) {
	port1 := &livePort{disconnectErr: errDeviceGone}
	port1.queue([]byte("booting...\n"))

	port2 := &livePort{}
	port2.queue([]byte("shell> "))

	opens := 0

	s := &Serial{Port: "/dev/ttyUSB0", Baud: DefaultBaudRate}
	s.open = func(_ string, _ int) (port, error) {
		opens++
		if opens == 1 {
			return port1, nil
		}

		return port2, nil
	}

	sess, stdout, _ := interactiveSession(t)

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)

	go func() { runErr <- s.Run(ctx, sess, "-i") }()

	waitFor(t, func() bool {
		g := stdout.String()

		return strings.Contains(g, "booting") &&
			strings.Contains(g, "reconnected") &&
			strings.Contains(g, "shell>")
	})

	cancel()

	select {
	case err := <-runErr:
		// Cancellation is the normal end of an interactive session (client
		// disconnect / quit); see runInteractive.
		if err != nil {
			t.Errorf("Run error = %v, want nil on clean end", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	if opens < 2 {
		t.Errorf("open called %d times, want >= 2 (reconnect)", opens)
	}

	checkMarkersOnce(t, stdout.String())
}

// TestRunExpectReconnectsOnDeviceLoss verifies the expect path survives the
// device disappearing mid-wait, reopens it, and matches the prompt that appears
// only after the reconnect (the firmware-CI power-cycle case).
func TestRunExpectReconnectsOnDeviceLoss(t *testing.T) {
	port1 := &fakePort{reads: [][]byte{[]byte("booting...\n")}, readErr: errDeviceGone}
	port2 := &fakePort{reads: [][]byte{[]byte("dut login: ")}}

	opens := 0

	s := &Serial{Port: "/dev/ttyUSB0", Baud: DefaultBaudRate}
	s.open = func(_ string, _ int) (port, error) {
		opens++
		if opens == 1 {
			return port1, nil
		}

		return port2, nil
	}

	rec := &recordingSession{}
	logs := &syncBuffer{}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ctx = log.Into(ctx, slog.New(slog.NewTextHandler(logs, nil)))

	if err := s.Run(ctx, rec, "expect", "login:"); err != nil {
		t.Fatalf("Run returned error, want nil: %v", err)
	}

	if opens < 2 {
		t.Errorf("open called %d times, want >= 2 (reconnect)", opens)
	}

	got := rec.out.String()
	for _, want := range []string{"booting", "disconnected", "reconnected", "matched"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q: %q", want, got)
		}
	}

	checkMarkersOnce(t, got)

	// The agent's own log records the loss too, for rigs nobody is watching.
	for _, want := range []string{"level=WARN msg=\"serial device disconnected", "level=INFO msg=\"serial device reconnected"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("agent log missing %q: %q", want, logs.String())
		}
	}
}

// TestRunReconnectAbortsOnTimeout verifies the expect timeout still fires while
// the module is waiting for a missing device to come back.
func TestRunReconnectAbortsOnTimeout(t *testing.T) {
	port1 := &fakePort{reads: [][]byte{[]byte("hello\n")}, readErr: errDeviceGone}

	opens := 0

	s := &Serial{Port: "/dev/ttyUSB0", Baud: DefaultBaudRate}
	s.open = func(_ string, _ int) (port, error) {
		opens++
		if opens == 1 {
			return port1, nil
		}

		return nil, errDeviceGone // device never comes back
	}

	err := s.Run(context.Background(), &recordingSession{}, "-t", "300ms", "expect", "will-never-appear")
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("Run error = %v, want timeout while reconnecting", err)
	}
}

// TestRunReconnectsWhenDeviceNodeVanishes covers the loss path real hardware
// hits: a removed adapter surfaces to Read as an idle timeout (NOT a distinct
// error), so the module must notice the vanished device node, wait for it to
// come back and carry on, instead of spinning forever on what looks like a
// benign idle.
func TestRunReconnectsWhenDeviceNodeVanishes(t *testing.T) {
	port1 := &fakePort{} // no queued data: idle reads return (0, nil), like a quiet port
	port2 := &fakePort{reads: [][]byte{[]byte("dut login: ")}}

	opens := 0

	s := &Serial{Port: "/dev/fake", Baud: DefaultBaudRate}
	s.deviceLossGrace = 20 * time.Millisecond    // suspect loss quickly in the test
	s.portPresent = func() bool { return false } // the device node has vanished
	s.open = func(_ string, _ int) (port, error) {
		opens++

		switch {
		case opens == 1:
			return port1, nil // initial open succeeds
		case opens <= 3:
			return nil, errDeviceGone // gone for the first two attempts
		default:
			return port2, nil // back again
		}
	}

	rec := &recordingSession{}

	err := s.Run(context.Background(), rec, "-t", "5s", "expect", "login:")
	if err != nil {
		t.Fatalf("Run error = %v, want nil (the prompt arrives after the reconnect)", err)
	}

	if opens != 4 {
		t.Errorf("open called %d times, want 4 (initial, two failed attempts, success)", opens)
	}

	checkMarkersOnce(t, rec.out.String())
}

// TestRunReconnectBacksOffOnPersistentReadError verifies that a device whose
// node survives but whose reads keep failing (a wedged adapter) is retried at
// reconnectInterval, not in a tight loop flooding the client with markers.
func TestRunReconnectBacksOffOnPersistentReadError(t *testing.T) {
	s := &Serial{Port: "/dev/ttyUSB0", Baud: DefaultBaudRate}
	s.open = func(_ string, _ int) (port, error) {
		return &fakePort{readErr: errDeviceGone}, nil // opens fine, every read fails
	}

	rec := &recordingSession{}

	err := s.Run(context.Background(), rec, "-t", "1200ms", "expect", "will-never-appear")
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("Run error = %v, want timeout", err)
	}

	// 1.2s at a 500ms interval allows two reconnects; a tight loop makes thousands.
	if got := strings.Count(rec.out.String(), "disconnected"); got > 3 {
		t.Errorf("%d disconnect markers in 1.2s, want <= 3 (reconnect must back off)", got)
	}
}

// TestRunQuietPortChecksNodeOncePerGrace verifies a quiet but healthy port is
// not stat'ed on every idle read: the node check runs at most once per grace
// period.
func TestRunQuietPortChecksNodeOncePerGrace(t *testing.T) {
	checks := 0

	s := &Serial{Port: "/dev/fake", Baud: DefaultBaudRate}
	s.deviceLossGrace = 50 * time.Millisecond
	s.portPresent = func() bool { checks++; return true }
	s.open = func(_ string, _ int) (port, error) {
		return &fakePort{}, nil // idle reads return (0, nil) immediately
	}

	err := s.Run(context.Background(), &recordingSession{}, "-t", "300ms", "expect", "will-never-appear")
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("Run error = %v, want timeout", err)
	}

	// 300ms at a 50ms grace allows about six checks; one per idle read is millions.
	if checks > 10 {
		t.Errorf("node checked %d times in 300ms, want <= 10 (once per grace period)", checks)
	}
}

// runInteractiveUntil runs s with args, cancels the session once cond holds on
// what the client received, and returns that output.
func runInteractiveUntil(t *testing.T, s *Serial, cond func(string) bool, args ...string) string {
	t.Helper()

	sess, stdout, _ := interactiveSession(t)

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)

	go func() { runErr <- s.Run(ctx, sess, args...) }()

	waitFor(t, func() bool { return cond(stdout.String()) })
	cancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("Run error = %v, want nil on clean end", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	return stdout.String()
}

// TestRunInteractiveKeepsScreenDrawing verifies that interactive mode passes on
// the sequences full-screen programs draw with, and removes a title change.
func TestRunInteractiveKeepsScreenDrawing(t *testing.T) {
	const drawn = "\x1b[2J\x1b[H\x1b[31mred\x1b[0m"

	p := &livePort{}
	p.queue([]byte(drawn + "\x1b]0;dut-title\x07\n"))

	s := &Serial{Port: "/dev/fake", Baud: DefaultBaudRate}
	s.open = func(_ string, _ int) (port, error) { return p, nil }

	got := runInteractiveUntil(t, s, func(out string) bool { return strings.Contains(out, "red") }, "-i")

	if !strings.Contains(got, drawn) {
		t.Errorf("output = %q, want the screen drawing %q kept", got, drawn)
	}

	if strings.Contains(got, "dut-title") {
		t.Errorf("output = %q, want the title change removed", got)
	}
}

// TestRunInteractiveKeepEscapesKeepsTitle verifies that -keep-escapes also
// passes on what interactive mode removes by default.
func TestRunInteractiveKeepEscapesKeepsTitle(t *testing.T) {
	const title = "\x1b]0;dut-title\x07"

	p := &livePort{}
	p.queue([]byte("red" + title + "\n"))

	s := &Serial{Port: "/dev/fake", Baud: DefaultBaudRate}
	s.open = func(_ string, _ int) (port, error) { return p, nil }

	got := runInteractiveUntil(t, s, func(out string) bool { return strings.Contains(out, "red") }, "-i", "-keep-escapes")

	if !strings.Contains(got, title) {
		t.Errorf("output = %q, want the title change %q kept with -keep-escapes", got, title)
	}
}

// TestRunInteractiveReportsDiscardedInput verifies that keys typed while the
// device is gone are discarded rather than replayed after the reconnect, and
// that the console says so once.
func TestRunInteractiveReportsDiscardedInput(t *testing.T) {
	port1 := &livePort{disconnectErr: errDeviceGone}
	port2 := &livePort{}

	var (
		opens atomic.Int32
		back  atomic.Bool // the device returns once the test allows it
	)

	s := &Serial{Port: "/dev/ttyUSB0", Baud: DefaultBaudRate}
	s.open = func(_ string, _ int) (port, error) {
		switch {
		case opens.Add(1) == 1:
			return port1, nil
		case back.Load():
			return port2, nil
		default:
			return nil, errDeviceGone
		}
	}

	sess, stdout, stdin := interactiveSession(t)

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)

	go func() { runErr <- s.Run(ctx, sess, "-i") }()

	waitFor(t, func() bool { return strings.Contains(stdout.String(), "disconnected") })

	if _, err := stdin.Write([]byte("typed-while-gone\r")); err != nil {
		t.Fatalf("write stdin: %v", err)
	}

	waitFor(t, func() bool { return strings.Contains(stdout.String(), "Input discarded") })
	back.Store(true)
	waitFor(t, func() bool { return strings.Contains(stdout.String(), "reconnected") })

	if _, err := stdin.Write([]byte("typed-after\r")); err != nil {
		t.Fatalf("write stdin: %v", err)
	}

	waitFor(t, func() bool { return port2.writtenString() == "typed-after\r" })
	cancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("Run error = %v, want nil on clean end", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	if n := strings.Count(stdout.String(), "Input discarded"); n != 1 {
		t.Errorf("discard note appears %d times, want once: %q", n, stdout.String())
	}
}
