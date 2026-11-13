// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/charmbracelet/x/vt"
	"golang.org/x/sys/unix"

	"github.com/BlindspotSoftware/dutctl/internal/test/pty"
	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// tuiAgent is a scripted agent with one device, demo-board, its commands
// and their help.
type tuiAgent struct {
	scriptedAgent
}

func (a *tuiAgent) List(context.Context, *connect.Request[pb.ListRequest]) (*connect.Response[pb.ListResponse], error) {
	return connect.NewResponse(&pb.ListResponse{Devices: []*pb.DeviceInfo{{Name: "demo-board"}}}), nil
}

func (a *tuiAgent) Commands(context.Context, *connect.Request[pb.CommandsRequest]) (*connect.Response[pb.CommandsResponse], error) {
	return connect.NewResponse(&pb.CommandsResponse{Commands: []string{"power", "console", "hello"}}), nil
}

func (a *tuiAgent) Details(_ context.Context, req *connect.Request[pb.DetailsRequest]) (*connect.Response[pb.DetailsResponse], error) {
	return connect.NewResponse(&pb.DetailsResponse{Details: "Help of " + req.Msg.GetCommand() + ".\n\nArguments:\n  who: whom"}), nil
}

// newTUIAgent returns an agent whose power prints without ending its line,
// whose console echoes in raw mode, and whose hello wants an argument; power
// answers powerErr, if set, instead.
func newTUIAgent(powerErr error) *tuiAgent {
	agent := &tuiAgent{}
	agent.run = func(stream runStreamServer) error {
		req, err := agent.receive(stream)
		if err != nil {
			return err
		}

		switch req.GetCommand().GetCommand() {
		case "power":
			if powerErr != nil {
				return powerErr
			}

			return stream.Send(&pb.RunResponse{Msg: &pb.RunResponse_Print{Print: &pb.Print{Text: []byte("power: on")}}})
		case "console":
			return agent.echoUntilEOF(stream, 1, pb.ConsoleMode_CONSOLE_MODE_RAW)
		default:
			return connect.NewError(connect.CodeInvalidArgument, errors.New("expected 1 argument(s) but got 0"))
		}
	}

	return agent
}

// tuiTerm is the TUI running in this process on a pseudo-terminal, its
// screen kept by a terminal emulator.
type tuiTerm struct {
	master, slave *os.File
	before        *unix.Termios
	state         string // XDG_STATE_HOME
	done          chan error

	mu      sync.Mutex
	emu     *vt.Emulator
	changed chan struct{}
}

// startTUI runs the TUI on a 100x30 pseudo-terminal with args.
func startTUI(t *testing.T, args ...string) *tuiTerm {
	t.Helper()

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")

	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	logger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(logger) })

	master, slave := pty.Open(t)
	pty.SetSize(t, slave, 100, 30)

	term := &tuiTerm{
		master:  master,
		slave:   slave,
		before:  pty.Termios(t, slave),
		state:   state,
		done:    make(chan error, 1),
		emu:     vt.NewEmulator(100, 30),
		changed: make(chan struct{}),
	}

	go term.read()
	// The emulator's answers to the TUI's queries. The copy outlives the
	// test: closing the emulator would race with its read.
	go func() { _, _ = io.Copy(master, term.emu) }()

	app := newApp(slave, slave, slave, func(int) {}, append([]string{"dutctl", "-log", "none"}, args...))
	app.setupRPCClient()

	go func() { term.done <- app.runTUI() }()

	t.Cleanup(func() {
		term.type_(t, "\x15\x04") // clear the line, quit
		select {
		case <-term.done:
		case <-time.After(5 * time.Second):
			t.Error("the TUI did not quit")
		}
	})

	return term
}

// read feeds what the TUI writes to the emulator.
func (s *tuiTerm) read() {
	buf := make([]byte, 4096)

	for {
		count, err := s.master.Read(buf)
		if count > 0 {
			s.mu.Lock()
			_, _ = s.emu.Write(buf[:count])
			close(s.changed)
			s.changed = make(chan struct{})
			s.mu.Unlock()
		}

		if err != nil {
			return
		}
	}
}

// text returns the terminal's scrollback and screen.
func (s *tuiTerm) text() (string, chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out strings.Builder

	for _, line := range s.emu.Scrollback().Lines() {
		out.WriteString(strings.TrimRight(line.String(), " ") + "\n")
	}

	out.WriteString(s.emu.String())

	return out.String(), s.changed
}

// waitFor waits until the terminal shows each of wants.
func (s *tuiTerm) waitFor(t *testing.T, wants ...string) {
	t.Helper()

	timeout := time.After(5 * time.Second)

	for {
		text, changed := s.text()

		missing := ""

		for _, want := range wants {
			if !strings.Contains(text, want) {
				missing = want

				break
			}
		}

		if missing == "" {
			return
		}

		select {
		case <-changed:
		case <-timeout:
			t.Fatalf("the terminal does not show %q:\n%s", missing, text)
		}
	}
}

// type_ types keys on the terminal.
func (s *tuiTerm) type_(t *testing.T, keys string) {
	t.Helper()

	if _, err := s.master.WriteString(keys); err != nil {
		t.Errorf("typing %q: %v", keys, err)
	}
}

// quit quits the TUI with ctrl+d and checks that it ended well, the
// terminal as it was.
func (s *tuiTerm) quit(t *testing.T) {
	t.Helper()

	s.type_(t, "\x15\x04")

	select {
	case err := <-s.done:
		s.done <- err // for the cleanup

		if err != nil {
			t.Errorf("the TUI ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the TUI did not quit on ctrl+d")
	}

	after := pty.Termios(t, s.slave)
	if after.Lflag != s.before.Lflag || after.Iflag != s.before.Iflag || after.Oflag != s.before.Oflag {
		t.Errorf("the terminal is not restored: lflag %#x/%#x iflag %#x/%#x oflag %#x/%#x",
			after.Lflag, s.before.Lflag, after.Iflag, s.before.Iflag, after.Oflag, s.before.Oflag)
	}
}

// startAtBoard runs the TUI against agent and enters demo-board.
func startAtBoard(t *testing.T, agent *tuiAgent) (*tuiTerm, string) {
	t.Helper()

	addr := serveAgent(t, agent).Listener.Addr().String()
	term := startTUI(t, "-s", addr)

	term.waitFor(t, "● "+addr+" · 1 device", addr+" ›")
	term.type_(t, "demo-board\r")
	term.waitFor(t, "demo-board ›", "Commands", "Help of power.")

	return term, addr
}

// S1: the start lists the recent devices and the hosts of the history, and
// the default host.
func TestTUITerminalStart(t *testing.T) {
	term := startTUI(t)

	// The first frame: the terminal is raw by now. A cooked one would take
	// the ctrl+r below as its own reprint key and never pass it on.
	term.waitFor(t, "dutctl ›")
	writeFile(t, filepath.Join(term.state, "dutctl", "history"), "1794470400\tlab:2024\tdemo-board\tconsole\traw\n")
	term.type_(t, "\x12") // ctrl+r reads the history again
	term.waitFor(t, "Recent devices", "demo-board", "console ⌨", "Hosts", "lab:2024", "localhost:2024", "dutctl ›")
	term.quit(t)
}

// S2, S3: a typed line runs and lands on the device, the menu folded and the
// line in the history; the result starts a line of its own after output that
// did not end its line. A raw console takes the terminal and gives it back,
// and the prompt takes keys again.
func TestTUITerminalRunAndConsole(t *testing.T) {
	agent := newTUIAgent(nil)
	term, addr := startAtBoard(t, agent)

	term.type_(t, "power on\r")
	term.waitFor(t, "● demo-board power on", "power: on\n⎿ done", "  › power · console · hello · lock · unlock")

	data, err := os.ReadFile(filepath.Join(term.state, "dutctl", "history"))
	if err != nil || strings.Count(string(data), addr) != 3 || !strings.Contains(string(data), "\tdemo-board\tpower on\t") {
		t.Errorf("history %q (%v), want the host, the device and the run", data, err)
	}

	term.type_(t, "console \r")
	term.waitFor(t, "▌ raw console")
	term.type_(t, "hi\x01x")
	term.waitFor(t, "▌ raw console closed", "· raw console", "  › console · power · hello")

	if inputs, _ := agent.inputs(); string(inputs[1]) != "hi" {
		t.Errorf("the console received %q, want hi", inputs[1])
	}

	term.type_(t, "po")
	term.waitFor(t, "demo-board › po")
	term.quit(t)
}

// The warnings of a run follow its output on lines of their own, also after
// output that did not end its line.
func TestTUITerminalWarningsAfterOutput(t *testing.T) {
	addr := serveAgent(t, newTUIAgent(nil)).Listener.Addr().String()
	term := startTUI(t, "-log", "warn", "-s", addr)

	term.waitFor(t, addr+" ›")
	term.type_(t, "demo-board\r")
	term.waitFor(t, "demo-board ›")
	term.type_(t, "lock 9h\r") // warns before the call, which this agent fails
	term.waitFor(t, "● demo-board lock 9h", "⎿ ✗")
	term.type_(t, "power on\r")
	term.waitFor(t, "power: on\n⚠ ", "\n  - requested a long lock duration", "⎿ done")
	term.quit(t)
}

// A raw console's end starts one fresh line, also after Print output that did
// not end its line.
func TestTUITerminalPrintInRawConsole(t *testing.T) {
	agent := newTUIAgent(nil)
	agent.run = func(stream runStreamServer) error {
		if _, err := agent.receive(stream); err != nil {
			return err
		}

		for _, res := range []*pb.RunResponse{
			openRes(1, pb.ConsoleMode_CONSOLE_MODE_RAW),
			{Msg: &pb.RunResponse_Print{Print: &pb.Print{Text: []byte("connecting")}}},
			closeRes(),
		} {
			if err := stream.Send(res); err != nil {
				return err
			}
		}

		return nil
	}

	term, _ := startAtBoard(t, agent)

	term.type_(t, "power on\r")
	term.waitFor(t, "connecting\n▌ raw console closed", "⎿ done")
	term.quit(t)
}

// S4: wrong arguments keep the line, with the help beside it.
func TestTUITerminalWrongArguments(t *testing.T) {
	term, _ := startAtBoard(t, newTUIAgent(nil))

	term.type_(t, "hello \r")
	term.waitFor(t, "⎿ failed", "expected 1 argument(s) but got 0", "demo-board › hello", "╎ Arguments:")
	term.quit(t)
}

// S5: a refused run keeps the line and says why and what to do.
func TestTUITerminalRefusedRun(t *testing.T) {
	refused := connect.NewError(connect.CodeFailedPrecondition, errors.New(`device "demo-board" is in use by "bob@desk"`))
	term, _ := startAtBoard(t, newTUIAgent(refused))

	term.type_(t, "power on\r")
	term.waitFor(t, "⎿ failed", "busy until that command returns", "demo-board › power on", "enter retries")
	term.quit(t)
}

// S6: an agent that is gone sends the prompt to the hosts, the host marked.
func TestTUITerminalUnreachableAgent(t *testing.T) {
	agent := newTUIAgent(nil)
	srv := serveAgent(t, agent)
	addr := srv.Listener.Addr().String()
	term := startTUI(t, "-s", addr)

	term.waitFor(t, addr+" ›")
	term.type_(t, "demo-board\r")
	term.waitFor(t, "demo-board ›")

	srv.CloseClientConnections()
	srv.Close()

	term.type_(t, "power on\r")
	term.waitFor(t, "cannot reach dutagent at "+addr, "dutctl ›", "unreachable · enter retries")
	term.quit(t)
}

// S7 needs a terminal that sends the process SIGINT: the integration test's.
func TestTUITerminalInterrupt(t *testing.T) {
	t.Skip("the pseudo-terminal is not the test process's controlling terminal, so Ctrl-C sends no SIGINT; " +
		"TestTUI in test/integration covers it")
}

// On a terminal, a bare dutctl opens the TUI instead of listing.
func TestDispatchOpensTheTUIOnATerminal(t *testing.T) {
	t.Setenv("TERM", "xterm")

	_, slave := pty.Open(t)
	fake := &fakeDeviceServiceClient{}
	app := newTestApp(t, fake)
	app.console = newConsole(slave, slave, slave, true)

	opened := false
	app.tui = func() error {
		opened = true

		return nil
	}

	if err := app.dispatch(); err != nil || !opened || fake.listCalls != 0 {
		t.Errorf("dispatch: %v, TUI opened %v, %d List calls", err, opened, fake.listCalls)
	}
}
