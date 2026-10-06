// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/BlindspotSoftware/dutctl/internal/chanio"
	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// errSessionClosed is returned by the module-facing methods when the session
// was torn down (its workers exited) before a transfer could complete. It is
// opaque and reported to the module as-is. Not meant to be matched.
var errSessionClosed = errors.New("session closed")

// backend implements the module.Session interface.
type backend struct {
	printCh   chan string
	consoleCh chan consoleEvent
	fileReqCh chan string
	// A file is represented by a channel of bytes. fileCh carries a file the
	// module sends to the client (SendFile to toClientWorker) and uploadCh one
	// the client sends to the module (fromClientWorker to RequestFile). The two
	// directions need channels of their own: on one shared channel, the
	// downstream worker, which always waits for files to send, could take an
	// upload meant for the module and echo it back to the client, while the
	// module's RequestFile waited for it until the session ended.
	fileCh   chan chan []byte
	uploadCh chan chan []byte

	// mu guards currentFile, cur and lastID. currentFile is read and written
	// from the module goroutine (SendFile) and from both broker workers, with
	// no channel handing it between them — their ordering runs through the
	// client round-trip, which is not a Go happens-before edge, so the field
	// needs its own lock. cur is swapped on the module goroutine and read by
	// the upstream worker for every input it delivers.
	mu sync.Mutex

	// currentFile holds the name of the file currently being transferred.
	// It names either the file the module requested from the client or the file
	// being sent back to the client, since only one transfer is in flight at a time.
	currentFile string

	// cur is the open console, nil while none is. lastID is the id of the
	// console opened last; each console of the run gets its own, so the client
	// marks its input for exactly one of them.
	cur    *console
	lastID uint32

	// log is the session-scoped logger, frozen in by the broker (see Broker.Start)
	// because the module.Session methods carry no context to derive it from.
	log *slog.Logger

	// done is closed when the broker's workers are torn down. The module-facing
	// methods select on it so a call blocked on a session channel whose worker peer has
	// exited unblocks — dropping output, or returning an error / io.EOF — instead
	// of wedging the module goroutine for the process lifetime. A nil done (a
	// backend built directly in a test) leaves the calls uncancellable.
	done <-chan struct{}
}

// console is one console of a session. Its channels are its own, created when
// it is opened: the input the client sends for it can never reach a later
// console, and output written after it ended never reaches the client.
type console struct {
	id   uint32
	mode module.ConsoleMode

	stdinCh  chan []byte
	stdoutCh chan []byte
	stderrCh chan []byte

	// eof is closed when the console's input ended: the user's input ended,
	// the module closed Stdin, or the console ended. closed is closed when the
	// console ended, by the module runner or by the session's teardown; the
	// writers fail from then on.
	eof       chan struct{}
	eofOnce   sync.Once
	closed    chan struct{}
	closeOnce sync.Once
}

func newConsole(id uint32, mode module.ConsoleMode) *console {
	return &console{
		id:       id,
		mode:     mode,
		stdinCh:  make(chan []byte),
		stdoutCh: make(chan []byte),
		stderrCh: make(chan []byte),
		eof:      make(chan struct{}),
		closed:   make(chan struct{}),
	}
}

// endInput ends the console's input: a Read on Stdin returns io.EOF.
func (c *console) endInput() {
	c.eofOnce.Do(func() { close(c.eof) })
}

// end ends the console: its input ends and its writers fail.
func (c *console) end() {
	c.endInput()
	c.closeOnce.Do(func() { close(c.closed) })
}

// stdout and stderr return the console's output channels, or nil for no
// console, so a select over them never fires while none is open.
func (c *console) stdout() chan []byte {
	if c == nil {
		return nil
	}

	return c.stdoutCh
}

func (c *console) stderr() chan []byte {
	if c == nil {
		return nil
	}

	return c.stderrCh
}

// consoleEvent tells toClientWorker that console cons opened (open) or ended.
type consoleEvent struct {
	cons *console
	open bool
}

// consoleStdin is a console's Stdin: the reader over its input channel, plus
// Close, which ends the input and leaves the console open for output.
type consoleStdin struct {
	io.Reader

	cons *console
}

func (r consoleStdin) Close() error {
	r.cons.endInput()

	return nil
}

// logger returns the session's scoped logger, falling back to the default if
// the broker has not set one (e.g. a session built directly in a test).
func (s *backend) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}

	return slog.Default()
}

// currentFileName returns the name of the in-flight file transfer, or "" if none.
func (s *backend) currentFileName() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.currentFile
}

// setCurrentFile records the name of the in-flight file transfer; "" clears it.
func (s *backend) setCurrentFile(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.currentFile = name
}

// currentConsole returns the open console, or nil while none is.
func (s *backend) currentConsole() *console {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.cur
}

// Print, Printf and Println forward a message to the client. The send is
// abandoned if the session has been torn down (done closed), so a module that
// keeps printing after its workers are gone is not wedged — the output is
// dropped, matching the fact that there is no longer a client to receive it.
func (s *backend) Print(a ...any) {
	select {
	case s.printCh <- fmt.Sprint(a...):
	case <-s.done:
	}
}

func (s *backend) Printf(format string, a ...any) {
	select {
	case s.printCh <- fmt.Sprintf(format, a...):
	case <-s.done:
	}
}

func (s *backend) Println(a ...any) {
	select {
	case s.printCh <- fmt.Sprintln(a...):
	case <-s.done:
	}
}

// OpenConsole opens the module's console (see module.Session). It must be
// called only from the module's Run goroutine. The event channel is always
// allocated by Broker.init before a module runs, so a nil one is a broken
// invariant and OpenConsole panics; the agent recovers that panic into a
// clean run error where it calls Module.Run.
func (s *backend) OpenConsole(opts module.ConsoleOptions) module.Console {
	if s.consoleCh == nil {
		panic("session.OpenConsole: session not initialized")
	}

	s.mu.Lock()
	s.lastID++
	cons := newConsole(s.lastID, opts.Mode)
	prev := s.cur
	s.cur = cons
	s.mu.Unlock()

	if prev != nil {
		s.endConsole(prev)
	}

	// Once the session is torn down, the console is over before it began: its
	// input is at its end and its writers fail. Otherwise the teardown ends it
	// later, from a goroutine that waits for whichever comes first.
	select {
	case <-s.done:
		cons.end()
	default:
		go func() {
			select {
			case <-s.done:
				cons.end()
			case <-cons.closed:
			}
		}()
	}

	s.sendConsoleEvent(consoleEvent{cons: cons, open: true})

	// The channels are fresh, so a failure here is a broken invariant, not a
	// runtime condition; see the doc comment.
	stdin, err := chanio.NewChanReader(cons.stdinCh, cons.eof, log.Scope(s.logger(), scopeSessionUpstream))
	if err != nil {
		panic(fmt.Sprintf("session.OpenConsole: stdin reader: %v", err))
	}

	stdout, err := chanio.NewChanWriter(cons.stdoutCh, cons.closed)
	if err != nil {
		panic(fmt.Sprintf("session.OpenConsole: stdout writer: %v", err))
	}

	stderr, err := chanio.NewChanWriter(cons.stderrCh, cons.closed)
	if err != nil {
		panic(fmt.Sprintf("session.OpenConsole: stderr writer: %v", err))
	}

	return module.Console{Stdin: consoleStdin{Reader: stdin, cons: cons}, Stdout: stdout, Stderr: stderr}
}

// closeConsole ends the open console, if any: the module runner calls it once
// a module returned. It is idempotent.
func (s *backend) closeConsole() {
	s.mu.Lock()
	cons := s.cur
	s.cur = nil
	s.mu.Unlock()

	if cons != nil {
		s.endConsole(cons)
	}
}

// endConsole ends cons and tells the client. The console's channels are ended
// first, so a writer still blocked on them fails rather than delivering output
// after the close.
func (s *backend) endConsole(cons *console) {
	cons.end()
	s.sendConsoleEvent(consoleEvent{cons: cons})
}

// sendConsoleEvent hands ev to toClientWorker, which sends ConsoleOpen or
// ConsoleClose, unless the session has been torn down.
func (s *backend) sendConsoleEvent(ev consoleEvent) {
	select {
	case s.consoleCh <- ev:
	case <-s.done:
	}
}

// RequestFile asks the client for the named file and returns a reader over its
// contents. It blocks until the client responds. The returned error is opaque
// (reported to the module as-is): it means the session was not initialized or the
// file stream could not be adapted, and is not meant to be matched.
func (s *backend) RequestFile(name string) (io.Reader, error) {
	if s.fileReqCh == nil {
		return nil, errors.New("session not initialized: file request channel is nil")
	}

	// Requesting and reading a file is the upstream (client → agent) flow.
	uplog := log.Scope(s.logger(), scopeSessionUpstream)
	uplog.Debug("module requested file", "name", name)

	// Send the file request to the client, then wait for the file. Both block on
	// a worker peer, so guard them with done: if the session is torn down first,
	// return rather than wedge the module goroutine.
	select {
	case s.fileReqCh <- name:
	case <-s.done:
		return nil, fmt.Errorf("request file %q: %w", name, errSessionClosed)
	}

	var file chan []byte

	select {
	case file = <-s.uploadCh:
	case <-s.done:
		return nil, fmt.Errorf("request file %q: %w", name, errSessionClosed)
	}

	// The received channel is fed and closed by fromClientWorker right after the
	// rendezvous, so this read always terminates: pass a nil done.
	r, err := chanio.NewChanReader(file, nil, uplog)
	if err != nil {
		return nil, fmt.Errorf("request file %q: %w", name, err)
	}

	return r, nil
}

// SendFile streams r to the client under the given name. It returns an error if a
// file transfer is already in progress or if reading r fails. The error is opaque
// (reported to the module as-is) and not meant to be matched.
func (s *backend) SendFile(name string, r io.Reader) error {
	if s.currentFileName() != "" {
		return fmt.Errorf("send file %q: a file request is already in progress", name)
	}

	content, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("send file %q: read source: %w", name, err)
	}

	// Sending a file to the client is the downstream (agent → client) flow.
	downlog := log.Scope(s.logger(), scopeSessionDownstream)
	downlog.Debug("module sending file", "name", name, "bytes", len(content))

	s.setCurrentFile(name)

	file := make(chan []byte, 1)

	// Hand the file to toClientWorker. Guard the send with done: if the session
	// is torn down first, return rather than wedge the module goroutine. The
	// buffered content send and close below never block once the rendezvous
	// succeeds.
	select {
	case s.fileCh <- file:
	case <-s.done:
		return fmt.Errorf("send file %q: %w", name, errSessionClosed)
	}

	file <- content

	close(file) // indicate EOF.

	return nil
}
