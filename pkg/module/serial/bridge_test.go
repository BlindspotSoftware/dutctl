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
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// fakeReadTimeout stands in for the port's read timeout: an asyncPort Read
// with nothing queued returns (0, nil) after this long.
const fakeReadTimeout = 2 * time.Millisecond

var errPortClosed = errors.New("fake port: read after close")

// asyncPort is a port for the bridge, which reads and writes it from two
// goroutines; fakePort has no lock, so it is not safe under -race, and it
// models an exhausted read queue as the end, where the bridge needs a port
// that keeps timing out. Reads come from a queue; with nothing queued, Read
// returns readErr if set, else (0, nil) after fakeReadTimeout like a port
// with a read timeout. emptied is closed the first time a Read found the queue
// empty, so a test can wait until the bridge consumed everything it queued.
type asyncPort struct {
	mu         sync.Mutex
	reads      [][]byte
	readErr    error
	writeErr   error // returned by every Write when set
	written    []byte
	closed     bool
	resetCount int

	emptied     chan struct{}
	emptiedOnce sync.Once
}

func newAsyncPort(reads ...[]byte) *asyncPort {
	return &asyncPort{reads: reads, emptied: make(chan struct{})}
}

func (p *asyncPort) Read(buf []byte) (int, error) {
	p.mu.Lock()

	if p.closed {
		p.mu.Unlock()

		return 0, errPortClosed
	}

	if len(p.reads) > 0 {
		chunk := p.reads[0]

		n := copy(buf, chunk)
		if n < len(chunk) {
			p.reads[0] = chunk[n:]
		} else {
			p.reads = p.reads[1:]
		}

		p.mu.Unlock()

		return n, nil
	}

	readErr := p.readErr
	p.mu.Unlock()

	p.emptiedOnce.Do(func() { close(p.emptied) })

	if readErr != nil {
		return 0, readErr
	}

	time.Sleep(fakeReadTimeout)

	return 0, nil
}

func (p *asyncPort) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.writeErr != nil {
		return 0, p.writeErr
	}

	p.written = append(p.written, data...)

	return len(data), nil
}

func (p *asyncPort) ResetInputBuffer() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.resetCount++

	return nil
}

func (p *asyncPort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true

	return nil
}

func (p *asyncPort) writtenBytes() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]byte(nil), p.written...)
}

func (p *asyncPort) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.closed
}

// syncBuffer is a bytes.Buffer safe for a writer and a reader on different
// goroutines: the bridge writes the console from two goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(data)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// trackedStdin is a console Stdin over a pipe that counts the Reads in flight,
// so a test can tell that the bridge's input goroutine, which is the only
// reader, returned before Run did: it is parked in Read until then.
type trackedStdin struct {
	*io.PipeReader

	inRead atomic.Int32
}

func (t *trackedStdin) Read(buf []byte) (int, error) {
	t.inRead.Add(1)
	defer t.inRead.Add(-1)

	return t.PipeReader.Read(buf)
}

// closedPipeWriter is a console writer whose session is gone.
type closedPipeWriter struct{}

func (closedPipeWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// interactiveRun is everything a test needs to drive Run in interactive mode
// against a fake port and a mock session: the user's input goes in through
// input, the DUT's output comes out of the port's read queue.
type interactiveRun struct {
	port   *asyncPort
	stdin  *trackedStdin
	input  *io.PipeWriter
	stdout *syncBuffer
	stderr *syncBuffer
	sess   *mock.Session
	serial *Serial
}

func newInteractiveRun(reads ...[]byte) *interactiveRun {
	reader, writer := io.Pipe()

	run := &interactiveRun{
		port:   newAsyncPort(reads...),
		stdin:  &trackedStdin{PipeReader: reader},
		input:  writer,
		stdout: &syncBuffer{},
		stderr: &syncBuffer{},
	}
	run.sess = &mock.Session{Stdin: run.stdin, Stdout: run.stdout, Stderr: run.stderr}
	run.serial = &Serial{
		Port:         "/dev/fake",
		Baud:         115200,
		drainTimeout: 10 * time.Millisecond,
		open:         func(_ string, _ int) (port, error) { return run.port, nil },
	}

	return run
}

// start runs the module in interactive mode on its own goroutine and returns
// the channel that carries Run's result.
func (r *interactiveRun) start(ctx context.Context, args ...string) <-chan error {
	done := make(chan error, 1)

	go func() { done <- r.serial.Run(ctx, r.sess, append([]string{"-i"}, args...)...) }()

	return done
}

// assertTornDown checks what every end of an interactive run must leave
// behind: the input goroutine returned (no Read in flight, the pipe closed),
// the port closed after it, and the markers on stderr.
func (r *interactiveRun) assertTornDown(t *testing.T) {
	t.Helper()

	if n := r.stdin.inRead.Load(); n != 0 {
		t.Errorf("%d Stdin reads in flight after Run returned; the input goroutine must be joined", n)
	}

	if _, err := r.input.Write([]byte("late")); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("input pipe write after Run = %v, want io.ErrClosedPipe (Stdin was not closed)", err)
	}

	if !r.port.isClosed() {
		t.Error("port was not closed")
	}

	want := "--- Connected to /dev/fake at 115200 baud ---\r\n\r\n--- Connection closed ---\r\n"
	if got := r.stderr.String(); got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestSerialRunInteractiveForwardsOutputVerbatim(t *testing.T) {
	chunks := [][]byte{
		[]byte("\x1b[31mred\x1b[0m "), // SGR colour, which scripted mode strips
		{0x9b},                        // a C1 CSI, no valid UTF-8
		{0xff, 0xfe, 0x00},            // invalid UTF-8 and NUL
		[]byte("\x1b["),               // a CSI split across two reads ...
		[]byte("6n"),                  // ... completed here
		[]byte("prompt> "),            // no trailing newline
	}
	run := newInteractiveRun(chunks...)

	done := run.start(context.Background())

	// Wait until the bridge consumed every queued chunk, then end the input;
	// the run ends by itself after the drain.
	<-run.port.emptied
	_ = run.input.Close()

	if err := <-done; err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	want := string(bytes.Join(chunks, nil))
	if got := run.stdout.String(); got != want {
		t.Errorf("stdout = %q, want the DUT bytes verbatim %q", got, want)
	}

	if strings.Contains(run.stdout.String(), "---") {
		t.Error("stdout holds a marker; markers belong on stderr only")
	}

	if !run.sess.ConsoleOpened || run.sess.ConsoleMode != module.ConsoleRaw {
		t.Errorf("console opened = %v, mode = %v; want a raw console", run.sess.ConsoleOpened, run.sess.ConsoleMode)
	}

	run.assertTornDown(t)
}

func TestSerialRunInteractiveForwardsInputVerbatim(t *testing.T) {
	run := newInteractiveRun()

	done := run.start(context.Background())

	// A pipe write returns once the bridge read it, so the order is the order
	// on the port.
	inputs := [][]byte{
		{0x03},            // Ctrl-C reaches the DUT
		[]byte("\x1b[A"),  // cursor up
		{0x7f},            // DEL
		[]byte("ls -l\r"), // Enter as CR, as a raw terminal sends it
	}

	for _, in := range inputs {
		if _, err := run.input.Write(in); err != nil {
			t.Fatalf("input write %q: %v", in, err)
		}
	}

	// The end of the user's input ends the run by itself, with nil, once the
	// drain elapsed; nothing else ends it here.
	_ = run.input.Close()

	if err := <-done; err != nil {
		t.Fatalf("Run = %v, want nil after the input ended", err)
	}

	want := string(bytes.Join(inputs, nil))
	if got := string(run.port.writtenBytes()); got != want {
		t.Errorf("port received %q, want the input verbatim %q", got, want)
	}

	run.assertTornDown(t)
}

func TestSerialRunInteractiveEndsOnCancel(t *testing.T) {
	run := newInteractiveRun()

	ctx, cancel := context.WithCancel(context.Background())
	done := run.start(ctx)

	// The input stays open: only the cancellation ends the run, and the bridge
	// must end the input itself to join its reader.
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run = %v, want nil on cancellation", err)
	}

	run.assertTornDown(t)
}

func TestSerialRunInteractiveEndsOnTimeout(t *testing.T) {
	run := newInteractiveRun([]byte("boot log\n"))

	done := run.start(context.Background(), "-t", "100ms")

	// The queued output is read within microseconds; wait for the port to
	// report it read before the deadline is relied on, so the assertion on
	// stdout does not race the deadline on a stalled runner.
	<-run.port.emptied

	if err := <-done; err != nil {
		t.Fatalf("Run = %v, want nil when -t elapses", err)
	}

	if got := run.stdout.String(); got != "boot log\n" {
		t.Errorf("stdout = %q, want %q", got, "boot log\n")
	}

	run.assertTornDown(t)
}

func TestSerialRunInteractivePortReadError(t *testing.T) {
	run := newInteractiveRun()
	run.port.readErr = errExhausted

	done := run.start(context.Background())

	err := <-done
	if !errors.Is(err, errExhausted) {
		t.Fatalf("Run = %v, want the port read error", err)
	}

	if !strings.HasPrefix(err.Error(), "interactive: ") {
		t.Errorf("error %q should name the mode", err)
	}

	run.assertTornDown(t)
}

func TestSerialRunInteractiveSessionGone(t *testing.T) {
	run := newInteractiveRun([]byte("x"))
	run.sess.Stdout = closedPipeWriter{}

	done := run.start(context.Background())

	// The first output write fails with io.ErrClosedPipe: the session is
	// gone, which ends the run like a cancellation.
	if err := <-done; err != nil {
		t.Fatalf("Run = %v, want nil once the session is gone", err)
	}

	run.assertTornDown(t)
}

func TestSerialRunInteractivePortWriteError(t *testing.T) {
	errWrite := errors.New("fake port: write failed")

	run := newInteractiveRun()
	run.port.writeErr = errWrite

	done := run.start(context.Background())

	// The failed write ends the input side; the run then ends after the
	// drain, and the bridge reports the write error, so a dead port is not
	// mistaken for the input's normal end.
	if _, err := run.input.Write([]byte("x")); err != nil {
		t.Fatalf("input write: %v", err)
	}

	err := <-done
	if !errors.Is(err, errWrite) {
		t.Fatalf("Run = %v, want the port write error", err)
	}

	run.assertTornDown(t)
}
