// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"golang.org/x/term"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// console is the client's end of the console a module has open. The receive
// routine drives it with the agent's events (opened, closed, output) and the
// send routine consults it (await, rawActive); the mutex keeps the two apart.
//
// Only the client knows whether its streams are terminals, so only it touches
// termios. A RAW console puts the terminal into raw mode when stdin is a
// terminal and the output format is text: every key then reaches the module
// as typed and the module's bytes reach the terminal as they are. Otherwise,
// and for a LINE console, the terminal is left as it is and input is forwarded
// as it is read. Raw mode ends with the console, and with the run on every
// path.
type console struct {
	stdin  io.Reader
	stdout io.Writer // raw console output goes here as it is, a terminal or not
	stderr io.Writer

	// term is the terminal the client's own housekeeping goes to while raw:
	// the local echo and the return to column 0. It is stdout when that is a
	// terminal, else stderr when that is, else nothing, so a redirected stdout
	// holds the module's bytes and nothing else.
	term io.Writer

	// outText and errText carry the client's own text, Print output,
	// diagnostics and the epilogue, and keep its lines at column 0 while raw
	// on the streams that are terminals.
	outText *crlfWriter
	errText *crlfWriter
	outTTY  bool
	errTTY  bool

	textFormat bool
	tty        bool // stdin is a terminal
	fd         int  // stdin's descriptor, valid when tty

	mu       sync.Mutex
	open     bool
	id       uint32
	raw      *term.State   // the terminal before raw mode; nil while cooked
	changed  chan struct{} // closed and renewed on every open and close, so await can wait for the next change
	eof      bool          // the user's input ended for good (a pipe at its end)
	finished bool          // the run is over; a late open event is ignored
	violated bool
	warned   bool
}

func newConsole(stdin io.Reader, stdout, stderr io.Writer, textFormat bool) *console {
	con := &console{
		stdin:      stdin,
		stdout:     stdout,
		stderr:     stderr,
		term:       io.Discard,
		outText:    &crlfWriter{w: stdout},
		errText:    &crlfWriter{w: stderr},
		outTTY:     isTTY(stdout),
		errTTY:     isTTY(stderr),
		textFormat: textFormat,
		changed:    make(chan struct{}),
	}
	con.fd, con.tty = ttyFd(stdin)

	switch {
	case con.outTTY:
		con.term = stdout
	case con.errTTY:
		con.term = stderr
	}

	return con
}

// opened handles ConsoleOpen: it records the console and, for a RAW console,
// switches a terminal to raw mode. It reports whether the user's input had
// already ended, so the caller tells the new console at once.
func (c *console) opened(consoleID uint32, mode pb.ConsoleMode) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.finished {
		return false
	}

	if c.open {
		c.closeLocked()
	}

	c.open = true
	c.id = consoleID

	if mode == pb.ConsoleMode_CONSOLE_MODE_RAW {
		c.enterRawLocked()
	}

	c.notifyLocked()

	return c.eof
}

// notifyLocked wakes every await: the console's state changed.
func (c *console) notifyLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

// enterRawLocked switches the terminal to raw mode for a RAW console. A pipe
// has no terminal to switch, and a structured output format cannot carry a
// raw console, so the console is served cooked then: input is forwarded as it
// is read.
func (c *console) enterRawLocked() {
	if !c.tty {
		return
	}

	if !c.textFormat {
		if !c.warned {
			slog.Warn("structured output: the console stays in line mode; use -f text for a raw console")

			c.warned = true
		}

		return
	}

	state, err := term.MakeRaw(c.fd)
	if err != nil {
		slog.Warn("cannot switch the terminal to raw mode; input stays line-buffered", "err", err)

		return
	}

	c.raw = state
	c.outText.on.Store(c.outTTY)
	c.errText.on.Store(c.errTTY)

	// The one way to end the session from the keyboard; said once per console,
	// on standard error, through the wrapper so a terminal gets its line ends.
	fmt.Fprint(c.errText, "\n[dutctl] raw console: Ctrl-A x ends it, Ctrl-A twice sends Ctrl-A, Ctrl-A e toggles local echo\n")
}

// closed handles ConsoleClose: raw mode ends with the console. It is safe to
// call without an open console.
func (c *console) closed() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closeLocked()
}

// finish ends the console with the run: the terminal is restored, and an
// open event still in flight from the receive routine changes nothing.
func (c *console) finish() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.finished = true
	c.closeLocked()
}

func (c *console) closeLocked() {
	if !c.open {
		return
	}

	c.open = false
	c.notifyLocked()

	if c.raw == nil {
		return
	}

	_ = term.Restore(c.fd, c.raw)
	c.raw = nil
	c.outText.on.Store(false)
	c.errText.on.Store(false)

	// Whatever the far end left behind, the next line, the epilogue or the
	// shell's prompt, starts at column 0.
	fmt.Fprint(c.term, "\r\n")
}

// current returns the id of the console that is open now, if any. It does not
// wait: an event the user caused, the end of the input or the quit sequence,
// is for the console open at that moment, and for none if there is none.
func (c *console) current() (uint32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.id, c.open
}

// inputEnded records that the user's input ended for good: a pipe at its end.
// Every console opened from now on is told at once.
func (c *console) inputEnded() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.eof = true
}

// output writes the module's console output to the terminal as it is while
// raw mode is on, and reports whether it did; otherwise the caller renders it.
// Output outside an open console breaks the protocol; it is reported once and
// rendered anyway, so nothing is lost. Output still in flight once the run
// finished is rendered without a word. The write happens outside the lock, so
// a terminal that stopped taking output cannot hold up the end of the run.
func (c *console) output(data []byte, isErr bool) bool {
	c.mu.Lock()

	if !c.open {
		if !c.violated && !c.finished {
			slog.Warn("console output without an open console: the agent breaks the protocol")

			c.violated = true
		}

		c.mu.Unlock()

		return false
	}

	if c.raw == nil {
		c.mu.Unlock()

		return false
	}

	out := c.stdout
	if isErr {
		out = c.stderr
	}

	c.mu.Unlock()

	_, _ = out.Write(data)

	return true
}

// rawActive reports whether the terminal is in raw mode.
func (c *console) rawActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.raw != nil
}

// await blocks until a console other than prev is open and returns its id.
// It reports false once ctx is done. A prev of 0 accepts any open console.
func (c *console) await(ctx context.Context, prev uint32) (uint32, bool) {
	for {
		c.mu.Lock()

		if c.open && c.id != prev {
			id := c.id
			c.mu.Unlock()

			return id, true
		}

		changed := c.changed
		c.mu.Unlock()

		select {
		case <-changed:
		case <-ctx.Done():
			return 0, false
		}
	}
}
