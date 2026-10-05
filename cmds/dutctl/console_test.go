// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// A console on a pipe never touches a terminal: a RAW console is served
// cooked, its output is left to the formatter, and the terminal state is
// untouched by open and close.
func TestConsoleOnPipeStaysCooked(t *testing.T) {
	var stdout, stderr bytes.Buffer

	con := newConsole(strings.NewReader(""), &stdout, &stderr, true)

	if con.tty {
		t.Fatal("a strings.Reader must not count as a terminal")
	}

	if con.opened(1, pb.ConsoleMode_CONSOLE_MODE_RAW) {
		t.Error("opened reported an ended input on a fresh console")
	}

	if con.rawActive() {
		t.Error("raw mode on without a terminal")
	}

	if con.output([]byte("x"), false) {
		t.Error("output written directly without raw mode; the formatter must render it")
	}

	con.closed()

	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("open/close wrote to the streams without a terminal: stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

// Output before any console opened is left to the formatter, so nothing is
// lost, even though it breaks the protocol.
func TestConsoleOutputWithoutOpen(t *testing.T) {
	con := newConsole(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, true)

	if con.output([]byte("stray"), false) {
		t.Error("output without an open console was swallowed")
	}
}

// await wakes once a console opens, waits for a different one after an input
// ended, and gives up when the run ends.
func TestConsoleAwait(t *testing.T) {
	con := newConsole(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, true)

	got := make(chan uint32, 1)

	go func() {
		id, ok := con.await(context.Background(), 0)
		if !ok {
			id = 0
		}

		got <- id
	}()

	select {
	case id := <-got:
		t.Fatalf("await returned %d before any console opened", id)
	case <-time.After(20 * time.Millisecond):
	}

	con.opened(1, pb.ConsoleMode_CONSOLE_MODE_LINE)

	select {
	case id := <-got:
		if id != 1 {
			t.Fatalf("await returned %d, want 1", id)
		}
	case <-time.After(time.Second):
		t.Fatal("await did not wake on the open")
	}

	// The same console does not satisfy a wait for the next one, and the wait
	// blocks instead of spinning: it wakes on the next state change, not on
	// the open that already happened.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	woke := make(chan uint32, 1)

	go func() {
		id, ok := con.await(ctx, 1)
		if !ok {
			id = 0
		}

		woke <- id
	}()

	select {
	case id := <-woke:
		t.Fatalf("await(prev=1) returned %d while console 1 is the open one", id)
	case <-time.After(20 * time.Millisecond):
	}

	con.closed()

	select {
	case id := <-woke:
		t.Fatalf("await(prev=1) returned %d on the close, with no console open", id)
	case <-time.After(20 * time.Millisecond):
	}

	con.opened(2, pb.ConsoleMode_CONSOLE_MODE_LINE)

	select {
	case id := <-woke:
		if id != 2 {
			t.Fatalf("await(prev=1) = %d, want 2", id)
		}
	case <-time.After(time.Second):
		t.Fatal("await(prev=1) did not wake on the open of console 2")
	}
}

// current reports the console open at the moment, and none after a close.
func TestConsoleCurrent(t *testing.T) {
	con := newConsole(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, true)

	if id, ok := con.current(); ok {
		t.Fatalf("current() = %d, true before any open", id)
	}

	con.opened(3, pb.ConsoleMode_CONSOLE_MODE_LINE)

	if id, ok := con.current(); !ok || id != 3 {
		t.Fatalf("current() = %d, %v; want 3, true", id, ok)
	}

	con.closed()

	if id, ok := con.current(); ok {
		t.Fatalf("current() = %d, true after the close", id)
	}
}

// Once the input ended for good, every later console is reported as such on
// open, and a late open after the run finished changes nothing.
func TestConsoleInputEndedAndFinish(t *testing.T) {
	con := newConsole(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, true)

	con.inputEnded()

	if !con.opened(1, pb.ConsoleMode_CONSOLE_MODE_LINE) {
		t.Error("opened must report the ended input")
	}

	con.finish()

	if con.opened(2, pb.ConsoleMode_CONSOLE_MODE_RAW) {
		t.Error("opened after finish must be ignored")
	}

	if _, ok := con.await(contextDone(), 0); ok {
		t.Error("await after finish must not find an open console")
	}
}

func contextDone() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	return ctx
}
