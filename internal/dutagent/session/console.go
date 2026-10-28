// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"fmt"
	"io"
	"sync"

	"github.com/BlindspotSoftware/dutctl/internal/chanio"
	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// console is one console of a session. Its channels are its own, created when
// it is opened: the input the client sends for it can never reach a later
// console, and output written after it ended never reaches the client.
type console struct {
	// id is the console's id within the run; the client marks its input with it.
	id uint32
	// mode is the mode the module opened the console in.
	mode module.ConsoleMode

	// stdinCh carries the client's input from fromClientWorker to Stdin.
	stdinCh chan []byte
	// stdoutCh carries what the module writes to Stdout to toClientWorker.
	stdoutCh chan []byte
	// stderrCh carries what the module writes to Stderr to toClientWorker.
	stderrCh chan []byte

	// eof is closed when the console's input ended: by the client, whose
	// input ended, by the module through Stdin.Close, or with the console.
	eof chan struct{}
	// eofOnce closes eof once.
	eofOnce sync.Once
	// closed is closed when the console ended: by the module runner once the
	// module returned, by a second OpenConsole or by the session's teardown.
	// The writers fail from then on.
	closed chan struct{}
	// closeOnce closes closed once.
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

// endInput ends the console's input: a Read on Stdin returns io.EOF, one that
// is blocked included. It is idempotent.
func (c *console) endInput() {
	c.eofOnce.Do(func() { close(c.eof) })
}

// end ends the console: its input ends and its writers fail with
// io.ErrClosedPipe. It is idempotent.
func (c *console) end() {
	c.endInput()
	c.closeOnce.Do(func() { close(c.closed) })
}

// stdout returns the console's standard output channel, or nil for no
// console, so a select over it never fires while none is open.
func (c *console) stdout() chan []byte {
	if c == nil {
		return nil
	}

	return c.stdoutCh
}

// stderr returns the console's standard error channel, or nil for no console,
// like stdout.
func (c *console) stderr() chan []byte {
	if c == nil {
		return nil
	}

	return c.stderrCh
}

// consoleEvent is what OpenConsole and endConsole hand to toClientWorker over
// consoleCh, which frames it for the client as ConsoleOpen or ConsoleClose.
type consoleEvent struct {
	console *console
	open    bool // the console opened; otherwise it ended
}

// consoleStdin is the io.ReadCloser behind module.Console.Stdin: the chanio
// reader over the console's input channel, with the Close the interface asks
// for. Close is a method of this wrapper and not of console because the two
// mean different things: closing Stdin ends the input and nothing else, while
// console.end also fails the writers, and a module must not be able to do the
// latter.
type consoleStdin struct {
	io.Reader

	console *console
}

// Close ends the console's input. It is idempotent and never fails.
func (in consoleStdin) Close() error {
	in.console.endInput()

	return nil
}

// OpenConsole opens the module's console (see module.Session). It must be
// called only from the module's Run goroutine: it panics on a broken
// invariant, a session Broker.init has not set up or a chanio adapter
// refusing a fresh channel, and the module runner recovers a panic on that
// goroutine into a run error, as it does for a module's own, so the agent
// does not crash.
func (s *backend) OpenConsole(opts module.ConsoleOptions) module.Console {
	if s.consoleCh == nil {
		panic("session.OpenConsole: session not initialized")
	}

	s.mu.Lock()
	s.lastConsoleID++
	opened := newConsole(s.lastConsoleID, opts.Mode)
	prev := s.currentConsole
	s.currentConsole = opened
	s.mu.Unlock()

	if prev != nil {
		s.endConsole(prev)
	}

	// Once the session is torn down, the console is over before it began: its
	// input is at its end and its writers fail. Otherwise a goroutine ends it
	// at the teardown, unless the console ended first; the check before it
	// covers a teardown that already happened, the way context.AfterFunc
	// treats a context that is already done. A module blocked in a Read or a
	// Write at the teardown needs this: nothing else runs on its behalf then.
	select {
	case <-s.done:
		opened.end()
	default:
		go func() {
			select {
			case <-s.done:
				opened.end()
			case <-opened.closed:
			}
		}()
	}

	s.sendConsoleEvent(consoleEvent{console: opened, open: true})

	// The channels are fresh, so these constructors cannot fail; a failure is
	// the broken invariant the doc comment of OpenConsole describes.
	stdin, err := chanio.NewChanReader(opened.stdinCh, opened.eof, log.Scope(s.logger(), scopeSessionUpstream))
	if err != nil {
		panic(fmt.Sprintf("session.OpenConsole: stdin reader: %v", err))
	}

	stdout, err := chanio.NewChanWriter(opened.stdoutCh, opened.closed)
	if err != nil {
		panic(fmt.Sprintf("session.OpenConsole: stdout writer: %v", err))
	}

	stderr, err := chanio.NewChanWriter(opened.stderrCh, opened.closed)
	if err != nil {
		panic(fmt.Sprintf("session.OpenConsole: stderr writer: %v", err))
	}

	return module.Console{Stdin: consoleStdin{Reader: stdin, console: opened}, Stdout: stdout, Stderr: stderr}
}

// closeConsole ends the open console, if any, and tells the client. It is
// idempotent.
func (s *backend) closeConsole() {
	s.mu.Lock()
	c := s.currentConsole
	s.currentConsole = nil
	s.mu.Unlock()

	if c != nil {
		s.endConsole(c)
	}
}

// endConsole ends c and tells the client. The console's channels are ended
// first, so a writer still blocked on them fails rather than delivering output
// after the close.
func (s *backend) endConsole(c *console) {
	c.end()
	s.sendConsoleEvent(consoleEvent{console: c})
}

// sendConsoleEvent hands ev to toClientWorker, which sends ConsoleOpen or
// ConsoleClose, unless the session has been torn down.
func (s *backend) sendConsoleEvent(ev consoleEvent) {
	select {
	case s.consoleCh <- ev:
	case <-s.done:
	}
}

// consoleFor returns the open console if id is its id, and nil otherwise: the
// client marks its input and control for one console, and what is marked for
// any other is dropped.
func (s *backend) consoleFor(id uint32) *console {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentConsole == nil || s.currentConsole.id != id {
		return nil
	}

	return s.currentConsole
}
