// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/BlindspotSoftware/dutctl/internal/test/pty"
	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// hookCall is one call of a console's onConsole hook.
type hookCall struct{ open, raw bool }

// hookRecorder records the onConsole calls of one run and says when a console
// opened raw.
type hookRecorder struct {
	mu      sync.Mutex
	calls   []hookCall
	rawOpen chan struct{}
}

func newHookRecorder() *hookRecorder {
	return &hookRecorder{rawOpen: make(chan struct{}, 1)}
}

func (h *hookRecorder) hook(open, raw bool) {
	h.mu.Lock()
	h.calls = append(h.calls, hookCall{open: open, raw: raw})
	h.mu.Unlock()

	if open && raw {
		h.rawOpen <- struct{}{}
	}
}

func (h *hookRecorder) seen() []hookCall {
	h.mu.Lock()
	defer h.mu.Unlock()

	return slices.Clone(h.calls)
}

// startRun runs command on a fresh console over app's streams, reporting to
// hooks, and returns the channel its result arrives on.
func startRun(app *application, hooks *hookRecorder, command string) <-chan error {
	con := app.console.forRun()
	con.onConsole = hooks.hook

	done := make(chan error, 1)

	go func() { done <- app.runWith(context.Background(), con, app.agent(), "dev", command, nil) }()

	return done
}

// awaitRun returns the result of a run, failing the test if it does not end
// in time.
func awaitRun(t *testing.T, done <-chan error) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("runWith did not return although the agent ended the run")

		return nil
	}
}

// Two runs share the terminal, as in a TUI. The first ends
// while its send routine reads the terminal for a line console; the keys
// typed during the second must reach the second's console, not a read the
// first one left behind. The terminal is raw only while the raw console is
// open, and the hook, not the raw hint, tells about each console.
func TestRunWithReleasesTheTerminal(t *testing.T) {
	master, slave := pty.Open(t)
	before := pty.Termios(t, slave)

	var runs atomic.Int32

	agent := &scriptedAgent{}
	agent.run = func(stream runStreamServer) error {
		if _, err := agent.receive(stream); err != nil {
			return err
		}

		if runs.Add(1) > 1 {
			return agent.echoUntilEOF(stream, 2, pb.ConsoleMode_CONSOLE_MODE_RAW)
		}

		// A console the run ends without the user's input ending.
		if err := stream.Send(openRes(1, pb.ConsoleMode_CONSOLE_MODE_LINE)); err != nil {
			return err
		}

		// Give the send routine time to reach its read before the run ends.
		time.Sleep(50 * time.Millisecond)

		return stream.Send(closeRes())
	}

	app, stdout, stderr := newConsoleApp(t, agent, slave, "")

	first := newHookRecorder()
	if err := awaitRun(t, startRun(app, first, "line")); err != nil {
		t.Fatalf("first run: %v", err)
	}

	if want := []hookCall{{open: true}, {open: false}}; !slices.Equal(first.seen(), want) {
		t.Errorf("first run's hook calls = %v, want %v", first.seen(), want)
	}

	second := newHookRecorder()
	done := startRun(app, second, "raw")

	select {
	case <-second.rawOpen:
	case <-time.After(5 * time.Second):
		t.Fatal("the second run's raw console did not open")
	}

	if raw := pty.Termios(t, slave); raw.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG) != 0 {
		t.Errorf("the terminal is not raw while the raw console is open: lflag %#x", raw.Lflag)
	}

	if _, err := master.Write([]byte("hi\x01x")); err != nil {
		t.Fatal(err)
	}

	if err := awaitRun(t, done); err != nil {
		t.Fatalf("second run: %v", err)
	}

	data, ended := agent.inputs()
	if got := string(data[2]); got != "hi" || len(data) != 1 {
		t.Errorf("agent received %q for console 2 and input for consoles %v, want only \"hi\" for console 2", got, data)
	}

	if !slices.Equal(ended, []uint32{2}) {
		t.Errorf("input ended for consoles %v, want [2]", ended)
	}

	if got := stdout.String(); got != "hi" {
		t.Errorf("stdout = %q, want the echo \"hi\"", got)
	}

	if want := []hookCall{{open: true, raw: true}, {open: false, raw: true}}; !slices.Equal(second.seen(), want) {
		t.Errorf("second run's hook calls = %v, want %v", second.seen(), want)
	}

	if bytes.Contains(stderr.Bytes(), []byte("Ctrl-A x")) {
		t.Errorf("the raw hint was printed although a hook is set: %q", stderr.String())
	}

	after := pty.Termios(t, slave)
	if after.Lflag != before.Lflag || after.Iflag != before.Iflag || after.Oflag != before.Oflag {
		t.Errorf("terminal not restored after the runs: lflag %#x/%#x iflag %#x/%#x oflag %#x/%#x",
			after.Lflag, before.Lflag, after.Iflag, before.Iflag, after.Oflag, before.Oflag)
	}
}

// An interrupted run reaches the agent as a cancellation, not as the end of
// the client's input: a module that ends on its input would otherwise end
// before the cancellation arrives, and the run would count as a success. The
// wrong order of the closes fails this test only when the client's HTTP/2
// body writer runs between them, as it does on a busy machine.
func TestRunWithInterruptedCancelsTheAgentsRun(t *testing.T) {
	ended := make(chan error, 1)

	agent := &scriptedAgent{}
	agent.run = func(stream runStreamServer) error {
		if _, err := agent.receive(stream); err != nil {
			return err
		}

		if err := stream.Send(openRes(1, pb.ConsoleMode_CONSOLE_MODE_LINE)); err != nil {
			return err
		}

		for {
			if _, err := agent.receive(stream); err != nil {
				ended <- err

				return nil
			}
		}
	}

	input, feed, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = input.Close(); _ = feed.Close() })

	app, _, _ := newConsoleApp(t, agent, input, "")

	opened := make(chan struct{}, 1)
	con := app.console.forRun()
	con.onConsole = func(open, _ bool) {
		if open {
			opened <- struct{}{}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- app.runWith(ctx, con, app.agent(), "dev", "cmd", nil) }()

	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("the console did not open")
	}

	cancel() // the signal

	if err := awaitRun(t, done); !errors.Is(err, errInterrupted) {
		t.Errorf("runWith = %v, want errInterrupted", err)
	}

	select {
	case err := <-ended:
		if errors.Is(err, io.EOF) {
			t.Errorf("the agent saw the end of the client's input, not a cancelled run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the agent's stream did not end")
	}
}

// A run cancelled while its send routine is held in a send, because the
// agent's module stopped reading its console, still returns: flow control
// holds the send until the stream is closed. The input is fed until a write
// to it times out, the sign that the routine no longer reads it.
func TestRunWithCancelledWhileASendIsHeld(t *testing.T) {
	release := make(chan struct{})

	agent := &scriptedAgent{}
	agent.run = func(stream runStreamServer) error {
		if _, err := agent.receive(stream); err != nil {
			return err
		}

		if err := stream.Send(openRes(1, pb.ConsoleMode_CONSOLE_MODE_LINE)); err != nil {
			return err
		}

		<-release // reads no input

		return nil
	}

	input, feed, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = input.Close(); _ = feed.Close() })

	app, _, _ := newConsoleApp(t, agent, input, "")
	t.Cleanup(func() { close(release) }) // runs before the server waits for the script

	opened := make(chan struct{}, 1)
	con := app.console.forRun()
	con.onConsole = func(open, _ bool) {
		if open {
			opened <- struct{}{}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- app.runWith(ctx, con, app.agent(), "dev", "cmd", nil) }()

	<-opened

	held := make(chan struct{})

	go func() {
		defer close(held)

		chunk := make([]byte, 64*1024)

		for {
			_ = feed.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
			if _, err := feed.Write(chunk); err != nil {
				return
			}
		}
	}()

	select {
	case <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("the input is still read: the agent's window never filled")
	}

	cancel() // the signal

	select {
	case err := <-done:
		if !errors.Is(err, errInterrupted) {
			t.Errorf("runWith = %v, want errInterrupted", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runWith did not return after the run was cancelled: its send routine is held in a send")
	}
}
