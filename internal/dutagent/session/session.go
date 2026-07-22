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
)

// errSessionClosed is returned by the module-facing methods when the session
// was torn down (its workers exited) before a transfer could complete. It is
// opaque and reported to the module as-is. Not meant to be matched.
var errSessionClosed = errors.New("session closed")

// backend implements the module.Session interface.
type backend struct {
	printCh   chan string
	stdinCh   chan []byte
	stdoutCh  chan []byte
	stderrCh  chan []byte
	fileReqCh chan string
	fileCh    chan chan []byte // a single file is represented by a channel of bytes

	// mu guards currentFile, which is read and written from the module goroutine
	// (SendFile) and from both broker workers, with no channel handing it between
	// them — their ordering runs through the client round-trip, which is not a Go
	// happens-before edge, so the field needs its own lock.
	mu sync.Mutex

	// currentFile holds the name of the file currently being transferred.
	// It names either the file the module requested from the client or the file
	// being sent back to the client, since only one transfer is in flight at a time.
	currentFile string

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

// Console returns the module's stdin/stdout/stderr streams (see module.Session).
// It must be called only from the module's Run goroutine, and it has no error
// return: the backing channels are always allocated by Broker.init before a module
// runs, so a nil channel here is a broken invariant and Console panics. runModule
// recovers that panic into a clean run error — do not add a top-level recover
// expecting to catch it, as a panic on another goroutine would be uncatchable.
//
//nolint:nonamedreturns
func (s *backend) Console() (stdin io.Reader, stdout, stderr io.Writer) {
	var (
		stdinReader                io.Reader
		stdoutWriter, stderrWriter io.Writer
		err                        error
	)

	// The channels are always initialized by Broker.init before a module runs,
	// so a failure here is a broken invariant (a nil channel), not a runtime
	// condition. Console has no error return, so panic; the module-execution
	// goroutine recovers it into a clean run error (see runModule).
	stdinReader, err = chanio.NewChanReader(s.stdinCh, s.done, log.Scope(s.logger(), scopeSessionUpstream))
	if err != nil {
		panic(fmt.Sprintf("session.Console: stdin reader: %v", err))
	}

	stdoutWriter, err = chanio.NewChanWriter(s.stdoutCh, s.done)
	if err != nil {
		panic(fmt.Sprintf("session.Console: stdout writer: %v", err))
	}

	stderrWriter, err = chanio.NewChanWriter(s.stderrCh, s.done)
	if err != nil {
		panic(fmt.Sprintf("session.Console: stderr writer: %v", err))
	}

	return stdinReader, stdoutWriter, stderrWriter
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
	case file = <-s.fileCh:
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
