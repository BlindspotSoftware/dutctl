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

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

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
// error), so the module must notice the vanished device node and start
// reconnecting instead of spinning forever on what looks like a benign idle.
func TestRunReconnectsWhenDeviceNodeVanishes(t *testing.T) {
	port1 := &fakePort{} // no queued data: idle reads return (0, nil), like a quiet port

	opens := 0

	s := &Serial{Port: "/dev/fake", Baud: DefaultBaudRate}
	s.deviceLossGrace = 20 * time.Millisecond    // suspect loss quickly in the test
	s.portPresent = func() bool { return false } // the device node has vanished
	s.open = func(_ string, _ int) (port, error) {
		opens++
		if opens == 1 {
			return port1, nil // initial open succeeds
		}

		return nil, errDeviceGone // device stays gone while reconnecting
	}

	rec := &recordingSession{}

	err := s.Run(context.Background(), rec, "-t", "300ms", "expect", "will-never-appear")
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("Run error = %v, want timeout", err)
	}

	if got := rec.out.String(); !strings.Contains(got, "disconnected, waiting to reconnect") {
		t.Errorf("expected reconnect to start after the device node vanished, got %q", got)
	}
}
