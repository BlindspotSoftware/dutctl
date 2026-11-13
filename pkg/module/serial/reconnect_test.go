// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
)

type openResult struct {
	port port
	err  error
}

func serialWithOpens(t *testing.T, results ...openResult) (*Serial, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	s := &Serial{
		Port: "/dev/serial/by-id/test-adapter", Baud: 57600,
		drainTimeout: 10 * time.Millisecond,
		open: func(name string, baud int) (port, error) {
			if name != "/dev/serial/by-id/test-adapter" || baud != 57600 {
				t.Errorf("open(%q, %d): settings changed across reconnect", name, baud)
			}
			idx := int(calls.Add(1)) - 1
			if idx >= len(results) {
				return nil, syscall.ENOENT
			}
			return results[idx].port, results[idx].err
		},
	}
	return s, calls
}

func waitReconnect(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for serial state")
		}
		time.Sleep(time.Millisecond)
	}
}

func awaitReconnectRun(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("serial Run did not terminate")
		return nil
	}
}

func TestParseReconnect(t *testing.T) {
	for _, args := range [][]string{
		{"-reconnect"}, {"-reconnect", "-i"}, {"-reconnect", "expect", "ready"},
	} {
		cfg, err := parseArgs(args)
		if err != nil || !cfg.reconnect {
			t.Errorf("parseArgs(%q) = %+v, %v", args, cfg, err)
		}
	}
	cfg, err := parseArgs([]string{"-reconnect=false", "-i"})
	if err != nil || cfg.reconnect {
		t.Errorf("explicit false = %+v, %v", cfg, err)
	}
}

func TestReconnectInitialAbsenceAndRepeatedFlaps(t *testing.T) {
	first := &fakePort{reads: [][]byte{[]byte("first boot\n")}, readErr: syscall.EIO}
	second := &fakePort{reads: [][]byte{[]byte("second boot\n")}, readErr: syscall.ENODEV}
	third := &fakePort{reads: [][]byte{[]byte("third boot READY")}}
	s, calls := serialWithOpens(t,
		openResult{err: syscall.ENOENT}, openResult{port: first}, openResult{err: syscall.ENXIO},
		openResult{port: second}, openResult{port: third})
	sess := &recordingSession{}
	err := s.Run(context.Background(), sess, "-reconnect", "-t", "3s", "expect", "READY")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 5 {
		t.Errorf("open calls = %d, want 5", calls.Load())
	}
	out := sess.out.String()
	for _, text := range []string{"first boot", "second boot", "third boot READY", "Script completed"} {
		if !strings.Contains(out, text) {
			t.Errorf("missing %q in %q", text, out)
		}
	}
	for text, want := range map[string]int{"Waiting for": 3, "Serial device disconnected:": 2, "reconnected:": 2} {
		if got := strings.Count(out, text); got != want {
			t.Errorf("%q count = %d, want %d: %q", text, got, want, out)
		}
	}
	if !strings.HasPrefix(out, "--- Waiting for") {
		t.Errorf("initially absent device claimed to be connected: %q", out)
	}
	for _, p := range []*fakePort{first, second, third} {
		if !p.closed || p.resetCount != 0 {
			t.Errorf("port closed=%v, flushes=%d; want closed without flushing boot bytes", p.closed, p.resetCount)
		}
	}
}

func TestReconnectDoesNotCarryExpectOrFilterState(t *testing.T) {
	tests := []struct {
		name, before, after, pattern string
		maps                         charMap
		wantError                    bool
		wantOutput                   string
	}{
		{"matcher", "foo", "bar", "foobar", charMap{}, true, "bar"},
		{"escape", "old\x1b]unfinished", "login:", "login:", charMap{}, false, "login:"},
		{"character map", "\r", "\nREADY", "\\r\\nREADY", charMap{crcrlf: true, lfcrlf: true}, false, "\r\nREADY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := &fakePort{reads: [][]byte{[]byte(tt.before)}, readErr: syscall.EIO}
			second := &fakePort{reads: [][]byte{[]byte(tt.after)}, readErr: errExhausted}
			s, _ := serialWithOpens(t, openResult{port: first}, openResult{port: second})
			s.imap = tt.maps
			sess := &recordingSession{}
			err := s.Run(context.Background(), sess, "-reconnect", "-t", "2s", "expect", tt.pattern)
			if tt.wantError && !errors.Is(err, errExhausted) || !tt.wantError && err != nil {
				t.Fatalf("Run = %v, wantError=%v", err, tt.wantError)
			}
			if !strings.Contains(sess.out.String(), tt.wantOutput) {
				t.Errorf("new output lost/filter state carried over: %q", sess.out.String())
			}
		})
	}
}

func TestReconnectDoesNotRepeatCompletedSteps(t *testing.T) {
	first := &fakePort{reads: [][]byte{[]byte("prompt")}, readErr: syscall.EIO}
	second := &fakePort{reads: [][]byte{[]byte("READY")}}
	s, _ := serialWithOpens(t, openResult{port: first}, openResult{port: second})
	err := s.Run(context.Background(), &mock.Session{}, "-reconnect", "-t", "2s",
		"expect", "prompt", "send", "reboot", "expect", "READY")
	if err != nil {
		t.Fatal(err)
	}
	if string(first.written) != "reboot\r" || len(second.written) != 0 {
		t.Errorf("command replayed: old=%q, new=%q", first.written, second.written)
	}
}

func TestReconnectInitialSendWaitsWithoutFlushing(t *testing.T) {
	p := &fakePort{reads: [][]byte{[]byte("READY")}}
	s, _ := serialWithOpens(t, openResult{err: syscall.ENOENT}, openResult{port: p})
	err := s.Run(context.Background(), &mock.Session{}, "-reconnect", "-t", "2s", "send", "go", "expect", "READY")
	if err != nil || string(p.written) != "go\r" || p.resetCount != 0 {
		t.Errorf("Run=%v, written=%q, flushes=%d", err, p.written, p.resetCount)
	}
}

type partialWritePort struct {
	fakePort
	calls int
	zero  bool
}

func (p *partialWritePort) Write(data []byte) (int, error) {
	p.calls++
	if p.zero {
		return 0, nil
	}
	if p.calls == 1 {
		p.written = append(p.written, data[0])
		return 1, nil
	}
	p.written = append(p.written, data[0])
	return 1, syscall.EIO
}

func TestReconnectNeverReplaysInterruptedSend(t *testing.T) {
	for _, zero := range []bool{false, true} {
		t.Run(fmt.Sprintf("zero=%v", zero), func(t *testing.T) {
			p := &partialWritePort{zero: zero}
			s, calls := serialWithOpens(t, openResult{port: p}, openResult{port: &fakePort{}})
			err := s.Run(context.Background(), &mock.Session{}, "-reconnect", "send", "reboot")
			if err == nil || !strings.Contains(err.Error(), "delivery uncertain") || !strings.Contains(err.Error(), "step 1") {
				t.Fatalf("Run = %v, want uncertain delivery at step 1", err)
			}
			if calls.Load() != 1 || !p.closed {
				t.Errorf("opens=%d, closed=%v; send must fail without reopening", calls.Load(), p.closed)
			}
			if !zero && string(p.written) != "re" {
				t.Errorf("written=%q, want only interrupted prefix", p.written)
			}
		})
	}
}

func TestReconnectGlobalDeadlineIncludesWaiting(t *testing.T) {
	for _, args := range [][]string{
		{}, {"expect", "READY"}, {"send", "go"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s, calls := serialWithOpens(t)
			start := time.Now()
			err := s.Run(context.Background(), &mock.Session{}, append([]string{"-reconnect", "-t", "40ms"}, args...)...)
			if (err != nil) != (len(args) > 0) {
				t.Errorf("Run=%v for %q; monitor deadline succeeds, unfinished steps fail", err, args)
			}
			if elapsed := time.Since(start); elapsed > 500*time.Millisecond || calls.Load() != 1 {
				t.Errorf("deadline ignored: elapsed=%s, opens=%d", elapsed, calls.Load())
			}
		})
	}
}

func TestReconnectDeadlineDoesNotRestartAfterFlap(t *testing.T) {
	first := &fakePort{readErr: syscall.EIO}
	second := &fakePort{readErr: syscall.EIO}
	s, calls := serialWithOpens(t, openResult{port: first}, openResult{port: second})
	start := time.Now()
	err := s.Run(context.Background(), &mock.Session{}, "-reconnect", "-t", "350ms", "expect", "READY")
	if err == nil || !strings.Contains(err.Error(), "timeout of 350ms") {
		t.Fatalf("Run=%v, want original global timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 650*time.Millisecond || calls.Load() != 2 {
		t.Errorf("timeout restarted: elapsed=%s, opens=%d", elapsed, calls.Load())
	}
}

func TestReconnectCancellationWithoutTimeout(t *testing.T) {
	s, calls := serialWithOpens(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, &mock.Session{}, "-reconnect", "expect", "READY") }()
	waitReconnect(t, func() bool { return calls.Load() > 0 })
	cancel()
	err := awaitReconnectRun(t, done)
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Errorf("Run=%v, want canceled expect", err)
	}
}

func TestReconnectFatalErrorsAreNotRetried(t *testing.T) {
	for _, failure := range []error{
		syscall.EACCES, syscall.EPERM, syscall.EBUSY, errors.New("bad baud rate"),
		&portConfigurationError{syscall.EIO},
	} {
		for _, onRead := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/read=%v", failure, onRead), func(t *testing.T) {
				result := openResult{err: fmt.Errorf("wrapped: %w", failure)}
				if onRead {
					result = openResult{port: &fakePort{readErr: result.err}}
				}
				s, calls := serialWithOpens(t, result)
				err := s.Run(context.Background(), &mock.Session{}, "-reconnect", "-t", "1s")
				if !errors.Is(err, failure) || calls.Load() != 1 {
					t.Errorf("Run=%v, opens=%d; fatal error must be returned immediately", err, calls.Load())
				}
			})
		}
	}
}

func TestSerialWithoutReconnectStillFailsImmediately(t *testing.T) {
	for _, onRead := range []bool{false, true} {
		t.Run(fmt.Sprintf("read=%v", onRead), func(t *testing.T) {
			result := openResult{err: syscall.ENOENT}
			if onRead {
				result = openResult{port: &fakePort{readErr: syscall.EIO}}
			}
			s, calls := serialWithOpens(t, result)
			err := s.Run(context.Background(), &mock.Session{})
			if err == nil || calls.Load() != 1 {
				t.Errorf("legacy Run=%v, opens=%d", err, calls.Load())
			}
		})
	}
}

func TestReconnectMonitorResumesOutput(t *testing.T) {
	first := &fakePort{reads: [][]byte{[]byte("before\n")}, readErr: syscall.EIO}
	second := &fakePort{reads: [][]byte{[]byte("after\n")}}
	s, _ := serialWithOpens(t, openResult{port: first}, openResult{port: second})
	sess := &recordingSession{}
	err := s.Run(context.Background(), sess, "-reconnect", "-t", "350ms")
	if err != nil || !strings.Contains(sess.out.String(), "after\n") || !second.closed {
		t.Errorf("Run=%v, output=%q, closed=%v", err, sess.out.String(), second.closed)
	}
}

func TestReconnectInteractiveDiscardsOfflineInput(t *testing.T) {
	run := newInteractiveRun([]byte("first\r\n"))
	second := newAsyncPort([]byte("second\r\n"))
	s, _ := serialWithOpens(t, openResult{port: run.port}, openResult{err: syscall.ENOENT}, openResult{port: second})
	run.serial = s
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := run.start(ctx, "-reconnect")
	t.Cleanup(func() { cancel(); _ = run.input.Close() })
	waitReconnect(t, func() bool { return strings.Contains(run.stdout.String(), "first") })
	_, err := run.input.Write([]byte("before\r"))
	if err != nil {
		t.Fatal(err)
	}
	waitReconnect(t, func() bool { return len(run.port.writtenBytes()) > 0 })
	run.port.mu.Lock()
	run.port.readErr = syscall.EIO
	run.port.mu.Unlock()
	waitReconnect(t, func() bool { return strings.Contains(run.stderr.String(), "Input while disconnected is discarded") })
	_, err = run.input.Write([]byte("must-not-replay\r"))
	if err != nil {
		t.Fatal(err)
	}
	waitReconnect(t, func() bool { return run.stdin.inRead.Load() > 0 })
	waitReconnect(t, func() bool { return strings.Contains(run.stdout.String(), "second") })
	_, err = run.input.Write([]byte("after\r"))
	if err != nil {
		t.Fatal(err)
	}
	waitReconnect(t, func() bool { return len(second.writtenBytes()) > 0 })
	cancel()
	if err := awaitReconnectRun(t, done); err != nil {
		t.Fatal(err)
	}
	if got := string(run.port.writtenBytes()); got != "before\r" {
		t.Errorf("old port input=%q", got)
	}
	if got := string(second.writtenBytes()); got != "after\r" {
		t.Errorf("new port input=%q; offline input must not be replayed", got)
	}
	if got := run.stdout.String(); got != "first\r\nsecond\r\n" {
		t.Errorf("stdout contains notices or lost bytes: %q", got)
	}
	markers := run.stderr.String()
	if strings.Contains(strings.ReplaceAll(markers, "\r\n", ""), "\n") {
		t.Errorf("raw terminal notice used bare LF: %q", markers)
	}
	if strings.Count(markers, "Waiting for") != 1 || strings.Count(markers, "reconnected:") != 1 {
		t.Errorf("duplicate/missing notices: %q", markers)
	}
	if run.stdin.inRead.Load() != 0 || !run.port.isClosed() || !second.isClosed() {
		t.Error("interactive teardown left an input reader or open port")
	}
	if !run.sess.ConsoleOpened || run.sess.PrintCalled {
		t.Error("interactive run must keep one console, with no Print output")
	}
}

func TestReconnectInteractiveAbsentDeviceCanExit(t *testing.T) {
	for _, end := range []string{"cancel", "eof", "deadline", "client gone", "fatal open"} {
		t.Run(end, func(t *testing.T) {
			run := newInteractiveRun()
			s, _ := serialWithOpens(t)
			run.serial = s
			if end == "client gone" {
				run.sess.Stderr = closedPipeWriter{}
			}
			if end == "fatal open" {
				run.serial, _ = serialWithOpens(t, openResult{err: syscall.EACCES})
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			args := []string{"-reconnect"}
			if end == "deadline" {
				args = append(args, "-t", "40ms")
			}
			done := run.start(ctx, args...)
			if end == "cancel" || end == "eof" {
				waitReconnect(t, func() bool { return strings.Contains(run.stderr.String(), "Waiting for") })
				if end == "cancel" {
					cancel()
				} else {
					_ = run.input.Close()
				}
			}
			err := awaitReconnectRun(t, done)
			if (err != nil) != (end == "fatal open") {
				t.Errorf("Run=%v, end=%s", err, end)
			}
			if run.stdin.inRead.Load() != 0 {
				t.Error("input goroutine outlived Run")
			}
			if _, err := run.input.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
				t.Errorf("stdin left open: %v", err)
			}
		})
	}
}

type gatedPort struct {
	*asyncPort
	readStarted, readEnd, readReturned chan struct{}
	writeStarted, writeEnd             chan struct{}
	reading, writing, unsafeClose      atomic.Bool
}

func (p *gatedPort) Read([]byte) (int, error) {
	if p.reading.CompareAndSwap(false, true) {
		close(p.readStarted)
	}
	<-p.readEnd
	close(p.readReturned)
	return 0, syscall.EIO
}

func (p *gatedPort) Write(data []byte) (int, error) {
	p.writing.Store(true)
	close(p.writeStarted)
	<-p.writeEnd
	_, _ = p.asyncPort.Write(data[:1])
	p.writing.Store(false)
	return 1, syscall.EIO
}

func (p *gatedPort) Close() error {
	if p.writing.Load() {
		p.unsafeClose.Store(true)
	}
	return p.asyncPort.Close()
}

func TestReconnectWaitsForInFlightInteractiveWrite(t *testing.T) {
	first := &gatedPort{
		asyncPort:   newAsyncPort(),
		readStarted: make(chan struct{}), readEnd: make(chan struct{}), readReturned: make(chan struct{}),
		writeStarted: make(chan struct{}), writeEnd: make(chan struct{}),
	}
	second := newAsyncPort([]byte("new boot"))
	run := newInteractiveRun()
	run.serial, _ = serialWithOpens(t, openResult{port: first}, openResult{port: second})
	ctx, cancel := context.WithCancel(context.Background())
	var readOnce, writeOnce sync.Once
	releaseRead := func() { readOnce.Do(func() { close(first.readEnd) }) }
	releaseWrite := func() { writeOnce.Do(func() { close(first.writeEnd) }) }
	t.Cleanup(func() { cancel(); releaseRead(); releaseWrite(); _ = run.input.Close() })
	done := run.start(ctx, "-reconnect")
	select {
	case <-first.readStarted:
	case <-time.After(time.Second):
		t.Fatal("read did not start")
	}
	_, err := run.input.Write([]byte("old-command\r"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("write did not start")
	}
	releaseRead()
	<-first.readReturned
	if first.isClosed() {
		t.Fatal("port closed under the in-flight write")
	}
	releaseWrite()
	waitReconnect(t, func() bool { return strings.Contains(run.stdout.String(), "new boot") })
	_, err = run.input.Write([]byte("new-command\r"))
	if err != nil {
		t.Fatal(err)
	}
	waitReconnect(t, func() bool { return len(second.writtenBytes()) > 0 })
	cancel()
	if err := awaitReconnectRun(t, done); err != nil {
		t.Fatal(err)
	}
	if first.unsafeClose.Load() || !first.isClosed() || !second.isClosed() {
		t.Error("unsafe close or port left open")
	}
	if string(first.writtenBytes()) != "o" || string(second.writtenBytes()) != "new-command\r" {
		t.Errorf("interrupted input replayed: old=%q, new=%q", first.writtenBytes(), second.writtenBytes())
	}
	if run.stdin.inRead.Load() != 0 {
		t.Error("input goroutine outlived Run")
	}
}

func TestReconnectFinalDrainHonorsGlobalDeadline(t *testing.T) {
	s, _ := serialWithOpens(t, openResult{port: &fakePort{}})
	s.drainTimeout = time.Second
	start := time.Now()
	err := s.Run(context.Background(), &mock.Session{}, "-reconnect", "-t", "40ms", "send", "go")
	if err != nil || time.Since(start) > 400*time.Millisecond {
		t.Errorf("final drain ignored global deadline: err=%v, elapsed=%s", err, time.Since(start))
	}
}

func TestReconnectFinalDrainDoesNotHideFatalReopenError(t *testing.T) {
	first := &fakePort{readErr: syscall.EIO}
	s, _ := serialWithOpens(t, openResult{port: first}, openResult{err: syscall.EACCES})
	s.drainTimeout = time.Second
	err := s.Run(context.Background(), &mock.Session{}, "-reconnect", "send", "reboot")
	if !errors.Is(err, syscall.EACCES) {
		t.Errorf("Run=%v, want fatal reopen error from final drain", err)
	}
}

func TestReconnectReadTimeoutIsNotDisconnection(t *testing.T) {
	s, calls := serialWithOpens(t, openResult{port: &fakePort{}})
	sess := &recordingSession{}
	err := s.Run(context.Background(), sess, "-reconnect", "-t", "20ms")
	if err != nil || calls.Load() != 1 || strings.Contains(sess.out.String(), "disconnected") {
		t.Errorf("quiet port treated as unplugged: err=%v, opens=%d, out=%q", err, calls.Load(), sess.out.String())
	}
}
