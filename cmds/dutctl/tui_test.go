// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/spinner"
	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/style"
	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"
	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// harness drives the TUI's model without a terminal, as bubbletea would:
// every command it returns runs at once and its message goes back in, so
// the fake agent answers in order. A run is recorded, never executed.
type harness struct {
	t     *testing.T
	m     *model
	fake  *fakeDeviceServiceClient
	clock time.Time

	hosts   []string // the hosts a client was made for
	printed []string // the lines printed above the prompt, without styles
	styled  []string // the same with their styles
	execs   int      // the runs handed the terminal
	quit    bool
}

// tuiFake is an agent with a free and a reserved device, two commands and
// a help text.
func tuiFake() *fakeDeviceServiceClient {
	return &fakeDeviceServiceClient{
		listDevices: []string{"agent", "demo-board"},
		listLocks: map[string]*pb.LockState{
			"demo-board": {Owner: "bob@desk", ExpiresAt: time.Now().Add(25 * time.Minute).Unix()},
		},
		commands: []string{"power", "console"},
		details:  "Switches the power.\n\nARGUMENTS:\n\ton|off",
		lock:     &pb.LockState{Owner: "ada@lab", ExpiresAt: time.Now().Add(2 * time.Hour).Unix()},
	}
}

// newHarness starts a TUI for ada@lab with lab:2024 as the -s host, the
// history holding lines, and a terminal of 100x30.
func newHarness(t *testing.T, fake *fakeDeviceServiceClient, lines ...string) *harness {
	t.Helper()

	return newHarnessCtx(context.Background(), t, fake, lines...)
}

func newHarnessCtx(ctx context.Context, t *testing.T, fake *fakeDeviceServiceClient, lines ...string) *harness {
	t.Helper()

	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	if len(lines) > 0 {
		writeFile(t, filepath.Join(state, "dutctl", "history"), strings.Join(lines, "\n")+"\n")
	}

	app := newTestApp(t, fake)
	app.serverAddr, app.user = "lab:2024", "ada@lab"
	app.logHandler = newCLIHandler(io.Discard, logModeNone, false)

	h := &harness{t: t, fake: fake, clock: time.Unix(1794470500, 0)}
	h.m = newModel(ctx, app, newSwitchWriter(io.Discard))
	h.m.newAgent = func(addr string) agentClient {
		h.hosts = append(h.hosts, addr)

		return agentClient{addr: addr, user: "ada@lab", client: fake}
	}
	h.m.clients = map[string]agentClient{}
	h.m.framed = true
	h.m.now = func() time.Time { return h.clock }
	h.update(tea.WindowSizeMsg{Width: 100, Height: 30})

	return h
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// update hands msg to the TUI and runs what it returns.
func (h *harness) update(msg tea.Msg) {
	h.t.Helper()

	_, cmd := h.m.Update(msg)
	h.run(cmd)
}

// run runs cmd and hands its message to the TUI, as bubbletea does, in
// order. Spinner ticks are dropped: they would tick for ever.
func (h *harness) run(cmd tea.Cmd) {
	h.t.Helper()

	if cmd == nil {
		return
	}

	msg := cmd()

	switch msg := msg.(type) {
	case nil, spinner.TickMsg:
		return
	case tea.BatchMsg:
		for _, next := range msg {
			h.run(next)
		}

		return
	case tea.QuitMsg:
		h.quit = true

		return
	}

	switch fmt.Sprintf("%T", msg) {
	case "tea.sequenceMsg":
		cmds := reflect.ValueOf(msg)
		for i := range cmds.Len() {
			next, _ := cmds.Index(i).Interface().(tea.Cmd)
			h.run(next)
		}
	case "tea.execMsg":
		h.execs++
	case "tea.printLineMessage":
		body := reflect.ValueOf(msg).FieldByName("messageBody").String()
		h.printed = append(h.printed, ansi.Strip(body))
		h.styled = append(h.styled, body)
	default:
		if !strings.HasPrefix(fmt.Sprintf("%T", msg), "tea.") {
			h.update(msg)
		}
	}
}

// press presses the keys named, in bubbletea's names.
func (h *harness) press(names ...string) {
	h.t.Helper()

	for _, name := range names {
		h.update(keyPress(name))
	}
}

// keyPress returns the key named as bubbletea names it.
func keyPress(name string) tea.KeyPressMsg {
	codes := map[string]rune{
		"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "up": tea.KeyUp, "down": tea.KeyDown,
		"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "backspace": tea.KeyBackspace,
	}

	if code, ok := codes[name]; ok {
		return tea.KeyPressMsg{Code: code}
	}

	if letter, ok := strings.CutPrefix(name, "ctrl+"); ok {
		return tea.KeyPressMsg{Code: rune(letter[0]), Mod: tea.ModCtrl}
	}

	return tea.KeyPressMsg{Code: rune(name[0]), Text: name}
}

// typ types text, key by key.
func (h *harness) typ(text string) {
	h.t.Helper()

	for _, r := range text {
		h.update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// view returns the live view without styles.
func (h *harness) view() string {
	return ansi.Strip(h.m.View().Content)
}

// highlighted returns the name of the highlighted row.
func (h *harness) highlighted() string {
	item, _ := h.m.menu.current(h.m.rows())

	return item.name
}

// atCommands enters demo-board of lab:2024.
func (h *harness) atCommands() {
	h.t.Helper()

	h.typ("lab:2024 demo-board")
	h.press("enter")

	if h.m.level != levelCommands || h.m.device != "demo-board" {
		h.t.Fatalf("not at demo-board's commands: level %d, device %q", h.m.level, h.m.device)
	}
}

func (h *harness) mustPrint(want string) {
	h.t.Helper()

	if !slices.ContainsFunc(h.printed, func(line string) bool { return strings.Contains(line, want) }) {
		h.t.Errorf("printed %q, want a line with %q", h.printed, want)
	}
}

// The key names the harness uses are bubbletea's.
func TestKeyPressNames(t *testing.T) {
	for _, name := range []string{"enter", "esc", "tab", "up", "down", "pgup", "pgdown", "ctrl+c", "ctrl+d", "ctrl+r", "?", "a"} {
		if got := keyPress(name).String(); got != name {
			t.Errorf("keyPress(%q).String() = %q", name, got)
		}
	}
}

// Enter walks from the host to its devices, their lock states shown, and a
// filter to the device's commands; a command's row goes into the line for
// its arguments and never runs; Enter on the line runs it.
func TestTUIWalksToARun(t *testing.T) {
	h := newHarness(t, tuiFake())

	if !strings.Contains(h.view(), "lab:2024") || h.m.level != levelHosts {
		t.Fatalf("start does not offer the -s host:\n%s", h.view())
	}

	h.press("enter")

	if h.m.level != levelDevices || h.fake.listCalls != 1 {
		t.Fatalf("Enter on the host: level %d, %d List calls", h.m.level, h.fake.listCalls)
	}

	h.mustPrint("● lab:2024 · 2 devices")

	if view := h.view(); !strings.Contains(view, `locked by "bob@desk" for 25m`) {
		t.Errorf("the reserved device has no lock note:\n%s", view)
	}

	h.typ("DEMO")

	if view := h.view(); strings.Contains(view, "agent") || !strings.Contains(view, "› demo-board") {
		t.Errorf("the filter DEMO does not show demo-board alone:\n%s", view)
	}

	h.press("enter")

	if h.m.level != levelCommands || !slices.Equal(h.fake.commandsCalls, []string{"demo-board"}) {
		t.Fatalf("Enter on demo-board: level %d, Commands calls %v", h.m.level, h.fake.commandsCalls)
	}

	if h.highlighted() != "power" || !strings.Contains(h.view(), "Switches the power.") {
		t.Errorf("highlight %q, want power with its description:\n%s", h.highlighted(), h.view())
	}

	h.press("enter")

	if h.m.input.Value() != "power " || h.execs != 0 {
		t.Fatalf("Enter on a command: line %q and %d runs, want \"power \" and none", h.m.input.Value(), h.execs)
	}

	if !strings.Contains(h.view(), "on|off") {
		t.Errorf("the help is not shown beside the arguments:\n%s", h.view())
	}

	h.typ("on")
	h.press("enter")

	if h.execs != 1 || !h.m.running || h.m.run.command != "power" || !slices.Equal(h.m.run.args, []string{"on"}) {
		t.Fatalf("Enter on the line: %d runs of %+v", h.execs, h.m.run)
	}

	if h.view() != "" {
		t.Errorf("the view is not empty while the run holds the terminal: %q", h.view())
	}
}

// The commands show at once; their descriptions fill in as the help arrives.
func TestTUIDescribesCommandsAsTheirHelpArrives(t *testing.T) {
	h := newHarness(t, tuiFake())
	h.atCommands()
	h.m.help = map[string]string{}

	if view := h.view(); !strings.Contains(view, "power") || strings.Contains(view, "Switches") {
		t.Fatalf("before the help: %s", view)
	}

	h.update(detailsMsg{host: "lab:2024", device: "demo-board", command: "console", text: "Command with 2 module(s): a, b"})
	h.update(detailsMsg{host: "lab:2024", device: "other", command: "power", text: "stale"})

	view := h.view()
	if !strings.Contains(view, "2 modules: a, b") || strings.Contains(view, "stale") {
		t.Errorf("descriptions after the help arrived:\n%s", view)
	}
}

// lock and unlock are rows after the commands, picked like a command: Enter on
// the row, Tab, or Enter on the typed word puts the keyword into the line, and
// only Enter on the line runs it.
func TestTUIKeywordRows(t *testing.T) {
	h := newHarness(t, tuiFake())
	h.atCommands()

	if names := names(h.m.menu.shown(h.m.rows())); names != "Commands,power,console,Device,lock,unlock" || h.highlighted() != "power" {
		t.Fatalf("the menu %q highlights %q, want the keywords last and the first command", names, h.highlighted())
	}

	h.press("down", "down")

	if h.highlighted() != "lock" {
		t.Fatalf("two down from power highlights %q, want lock", h.highlighted())
	}

	h.press("enter")

	if h.m.input.Value() != "lock " || len(h.fake.lockReqs) != 0 || !strings.Contains(h.view(), "reserve the device for you") {
		t.Fatalf("Enter on lock: line %q, %d Lock calls, view:\n%s", h.m.input.Value(), len(h.fake.lockReqs), h.view())
	}

	h.press("enter")

	if len(h.fake.lockReqs) != 1 {
		t.Fatalf("Enter on the line lock: %d Lock calls, want 1", len(h.fake.lockReqs))
	}

	h.mustPrint("⎿ ✓ locked by you")

	h.typ("unl")
	h.press("tab")

	if h.m.input.Value() != "unlock " {
		t.Fatalf("Tab on unl: line %q, want \"unlock \"", h.m.input.Value())
	}

	h.press("enter")
	h.typ("unlock")
	h.press("enter")

	if h.m.input.Value() != "unlock " || len(h.fake.unlockCalls) != 1 {
		t.Fatalf("Enter on the typed word unlock: line %q, %d Unlock calls; want the keyword in the line and one call",
			h.m.input.Value(), len(h.fake.unlockCalls))
	}

	h.press("enter")

	if len(h.fake.unlockCalls) != 2 {
		t.Errorf("Enter on the line unlock: %d Unlock calls, want 2", len(h.fake.unlockCalls))
	}
}

// After a run the menu folds to one line, the strip, so the run's output stays
// in sight. The first move unfolds it without moving the highlight; Enter and
// Tab act as unfolded, and typing unfolds it.
func TestTUIFoldsTheMenuAfterARun(t *testing.T) {
	h := newHarness(t, tuiFake())
	h.atCommands()
	h.press("down")
	h.runPower(nil)

	view := h.view()
	if !h.m.menu.folded || !strings.Contains(view, "╯\n  › power · console · lock · unlock\n  enter pick · ↑↓ list · tab complete") {
		t.Fatalf("after a run, folded %v:\n%s", h.m.menu.folded, view)
	}

	h.press("down")

	if h.m.menu.folded || h.highlighted() != "power" || !strings.Contains(h.view(), "Commands") {
		t.Fatalf("the first down: folded %v on %q:\n%s", h.m.menu.folded, h.highlighted(), h.view())
	}

	h.press("down")

	if h.highlighted() != "console" {
		t.Errorf("the second down highlights %q, want console", h.highlighted())
	}

	h.runPower(nil)
	h.press("enter")

	if h.m.input.Value() != "power " || h.m.menu.folded || !strings.Contains(h.view(), "╎ ARGUMENTS:") {
		t.Errorf("Enter while folded: line %q, folded %v:\n%s", h.m.input.Value(), h.m.menu.folded, h.view())
	}

	h.press("esc")
	h.runPower(nil)
	h.press("tab")

	if h.m.input.Value() != "power " || h.m.menu.folded {
		t.Errorf("Tab while folded: line %q, folded %v", h.m.input.Value(), h.m.menu.folded)
	}

	h.press("esc")
	h.runPower(nil)
	h.typ("c")

	if h.m.menu.folded || h.highlighted() != "console" {
		t.Errorf("typing while folded: folded %v on %q", h.m.menu.folded, h.highlighted())
	}
}

// The strip is cut at the width, and folds the device's rows only: a refused
// run keeps its line, shown as the command's row alone without the help, and
// wrong arguments keep the menu open with the help, which is the fix.
func TestTUIFoldedViews(t *testing.T) {
	h := newHarness(t, tuiFake())
	h.atCommands()
	h.runPower(nil)
	h.update(tea.WindowSizeMsg{Width: 25, Height: 30})

	if strip := strings.Split(h.view(), "\n")[1]; strip != "  › power · console · l…" {
		t.Errorf("the strip at 25 columns: %q", strip)
	}

	h.update(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.runPower(connect.NewError(connect.CodeFailedPrecondition, errors.New(`device "demo-board" is locked by "bob@desk" for 25m`)))

	if view := h.view(); !h.m.menu.folded || !strings.Contains(view, "╯\n  › power           Switches the power.\n  enter retries") {
		t.Errorf("a refused run, folded %v:\n%s", h.m.menu.folded, view)
	}

	h.press("esc")
	h.runPower(connect.NewError(connect.CodeInvalidArgument, errors.New("expected 1 argument(s) but got 2")))

	if view := h.view(); h.m.menu.folded || !strings.Contains(view, "╎ ARGUMENTS:") {
		t.Errorf("wrong arguments, folded %v:\n%s", h.m.menu.folded, view)
	}
}

// The answer of lock, unlock or help folds the menu too; after help the
// highlight is on the command.
func TestTUIFoldsAfterAnAnswer(t *testing.T) {
	h := newHarness(t, tuiFake())
	h.atCommands()
	h.typ("console help")
	h.press("enter")

	if !h.m.menu.folded || h.highlighted() != "console" {
		t.Errorf("after console help: folded %v on %q", h.m.menu.folded, h.highlighted())
	}

	h.typ("lock")
	h.press("enter", "enter")

	if !h.m.menu.folded {
		t.Error("after lock the menu is open")
	}
}

// The help shows while the arguments are typed, never while the menu is
// browsed; cut short, by its limit or by a short terminal, its last line says
// how to see all of it.
func TestTUIHelpBlock(t *testing.T) {
	fake := tuiFake()
	fake.details = "Switches the power.\n\nARGUMENTS:\n\ton|off\n\nEXAMPLES:\n\ton\n\toff\n\tcycle\n\ttoggle\n\tstatus"
	h := newHarness(t, fake)
	h.atCommands()

	if strings.Contains(h.view(), "╎") {
		t.Errorf("a help block while browsing:\n%s", h.view())
	}

	h.typ("power ")

	view := h.view()
	if strings.Count(view, "╎") != helpEntryLines || !strings.Contains(view, "╎ … power help prints all of it\n") {
		t.Errorf("the help of a long command while typing its arguments:\n%s", view)
	}

	h.update(tea.WindowSizeMsg{Width: 100, Height: 9})

	view = h.view()
	if strings.Count(view, "\n") > 8 || !strings.Contains(view, "╎   on|off\n  ╎ … power help prints all of it\n  enter run") {
		t.Errorf("the help at 9 rows:\n%s", view)
	}
}

// The hosts are listed newest first, a default host never visited last.
func TestTUIHostsNewestFirst(t *testing.T) {
	h := newHarness(t, tuiFake(), "1794470400\tbench:2024\t\t\t", "1794470450\trig:2024\t\t\t")

	if got := names(h.m.rows()); got != "Hosts,rig:2024,bench:2024,lab:2024" || !strings.Contains(h.view(), "default · never") {
		t.Errorf("the hosts %q:\n%s", got, h.view())
	}
}

// A second Enter read with the first, before the run took the terminal, runs
// nothing: the line is gone and the keys wait for the prompt.
func TestTUIRunsALineOnce(t *testing.T) {
	h := newHarness(t, tuiFake())
	h.atCommands()
	h.typ("power on")
	h.press("enter", "enter")

	if h.execs != 1 || h.m.input.Value() != "" {
		t.Errorf("Enter twice: %d runs, line %q; want one run and an empty line", h.execs, h.m.input.Value())
	}
}

// Keys typed while the TUI waits for the agent act once its answer is in,
// in order: a line typed ahead runs whole. Esc drops them with the call.
func TestTUIKeepsKeysTypedAhead(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		h := newHarness(t, tuiFake())
		h.press("enter")
		h.typ("demo-board")

		_, fetch := h.m.Update(keyPress("enter")) // Commands, answered below
		h.typ("power on")
		h.press("enter")

		if cancel {
			h.press("esc")
		}

		h.run(fetch)

		switch {
		case cancel && (h.execs != 0 || h.m.level != levelDevices):
			t.Errorf("after Esc: %d runs at level %d, want none at the devices", h.execs, h.m.level)
		case !cancel && (h.execs != 1 || h.m.run.line != "power on"):
			t.Errorf("typed ahead: %d runs of %+v, want \"power on\" once", h.execs, h.m.run)
		}
	}
}

// runPower runs "power on" at the device and lands with err as the run's
// outcome.
func (h *harness) runPower(err error) {
	h.t.Helper()

	h.typ("power on")
	h.press("enter")

	h.m.run.err = err
	h.update(runDoneMsg{cmd: h.m.run})
}

// startPower runs "power on" on demo-board and returns the run.
func startPower(t *testing.T, h *harness) *runCmd {
	t.Helper()

	h.atCommands()
	h.typ("power on")
	h.press("enter")

	if h.m.run == nil || !h.m.running {
		t.Fatal("no run started")
	}

	return h.m.run
}

// After a run the TUI lands where the next step is, per outcome.
func TestTUILanding(t *testing.T) {
	refused := connect.NewError(connect.CodeFailedPrecondition, errors.New(`device "demo-board" is in use by "bob@desk"`))

	tests := []struct {
		name     string
		err      error
		level    level
		line     string
		high     string
		note     string
		recorded bool
		folded   bool
		lists    int // List calls after the run
		commands int // Commands calls after the run
	}{
		{name: "done", level: levelCommands, high: "power", recorded: true, folded: true, lists: 1},
		{name: "interrupted", err: errInterrupted, level: levelCommands, high: "power", recorded: true, folded: true, lists: 1},
		{
			name: "bad arguments", err: connect.NewError(connect.CodeInvalidArgument, errors.New("expected 1 argument(s) but got 2")),
			level: levelCommands, line: "power on", high: "power",
		},
		{
			name: "refused", err: refused, level: levelCommands, line: "power on", high: "power", note: "enter retries",
			recorded: true, folded: true, lists: 1,
		},
		{
			name: "module failed", err: connect.NewError(connect.CodeAborted, errors.New("module failed: no")),
			level: levelCommands, high: "power", recorded: true, folded: true, lists: 1,
		},
		{
			name: "command gone", err: connect.NewError(connect.CodeNotFound, errors.New(`device "demo-board", command "power": no such command`)),
			level: levelCommands, high: "power", lists: 1, commands: 1,
		},
		{
			name: "device gone", err: connect.NewError(connect.CodeNotFound, errors.New(`device "demo-board": no such device`)),
			level: levelDevices, high: "agent", lists: 1,
		},
		{
			name: "agent gone", err: connect.NewError(connect.CodeUnavailable, errors.New("connection refused")),
			level: levelHosts, high: "lab:2024",
		},
		{
			name: "anonymous", err: connect.NewError(connect.CodeUnauthenticated, errors.New("who are you")),
			level: levelCommands, high: "power", folded: true,
		},
		{
			name: "client side", err: errors.New("reading requested file"), level: levelCommands, high: "power",
			recorded: true, folded: true, lists: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tuiFake())
			run := startPower(t, h)
			lists, commands, entries := h.fake.listCalls, len(h.fake.commandsCalls), len(h.m.hist.entries)

			run.err = tt.err
			h.update(runDoneMsg{cmd: run})

			if h.m.running || h.m.level != tt.level || h.highlighted() != tt.high || h.m.note != tt.note {
				t.Errorf("landed at level %d on %q with note %q, want level %d on %q with %q",
					h.m.level, h.highlighted(), h.m.note, tt.level, tt.high, tt.note)
			}

			if got := strings.TrimSpace(h.m.input.Value()); got != tt.line {
				t.Errorf("line %q, want %q", got, tt.line)
			}

			if recorded := len(h.m.hist.entries) > entries; recorded != tt.recorded {
				t.Errorf("recorded %v, want %v", recorded, tt.recorded)
			}

			if h.m.menu.folded != tt.folded {
				t.Errorf("folded %v, want %v", h.m.menu.folded, tt.folded)
			}

			if got := h.fake.listCalls - lists; got != tt.lists {
				t.Errorf("%d List calls after the run, want %d", got, tt.lists)
			}

			if got := len(h.fake.commandsCalls) - commands; got != tt.commands {
				t.Errorf("%d Commands calls after the run, want %d", got, tt.commands)
			}
		})
	}
}

// The unreachable host is marked at the hosts level, and Enter retries it.
func TestTUIMarksAnUnreachableHost(t *testing.T) {
	fake := tuiFake()
	fake.listErr = connect.NewError(connect.CodeUnavailable, errors.New("connection refused"))
	h := newHarness(t, fake)

	h.press("enter")

	h.mustPrint("✗ cannot reach dutagent at lab:2024 (connection refused)")

	if h.m.level != levelHosts || !strings.Contains(h.view(), "unreachable · enter retries") {
		t.Fatalf("after an unreachable host:\n%s", h.view())
	}

	fake.listErr = nil
	h.press("enter")

	if h.m.level != levelDevices || strings.Contains(h.view(), "unreachable") {
		t.Errorf("Enter does not retry the host:\n%s", h.view())
	}
}

// A run interrupted because the TUI's context ended, by SIGTERM or SIGHUP,
// ends the TUI as interrupted.
func TestTUIQuitsWhenItsContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := newHarnessCtx(ctx, t, tuiFake())
	run := startPower(t, h)

	cancel()

	run.err = errInterrupted
	h.update(runDoneMsg{cmd: run})

	if !h.quit || !errors.Is(h.m.exitErr, errInterrupted) || h.view() != "" {
		t.Errorf("quit %v with %v, view %q; want an interrupted quit and no view", h.quit, h.m.exitErr, h.view())
	}
}

// Esc clears the line, then goes up a level at a time, the highlight on the
// row the prompt came from; at the hosts it does nothing.
func TestTUIEscapeGoesUp(t *testing.T) {
	h := newHarness(t, tuiFake())
	h.atCommands()
	h.typ("pow")

	h.press("esc")

	if h.m.input.Value() != "" || h.m.level != levelCommands {
		t.Fatalf("the first Esc: line %q, level %d", h.m.input.Value(), h.m.level)
	}

	h.press("esc")

	if h.m.level != levelDevices || h.highlighted() != "demo-board" {
		t.Fatalf("Esc at the device: level %d on %q", h.m.level, h.highlighted())
	}

	h.press("esc")

	if h.m.level != levelHosts || h.highlighted() != "lab:2024" {
		t.Fatalf("Esc at the host: level %d on %q", h.m.level, h.highlighted())
	}

	h.press("esc")

	if h.m.level != levelHosts || h.quit {
		t.Errorf("Esc at the hosts: level %d, quit %v", h.m.level, h.quit)
	}
}

// ctrl+c clears the line, then arms; a second press within two seconds
// quits, a later one arms again.
func TestTUICtrlC(t *testing.T) {
	h := newHarness(t, tuiFake())
	h.typ("x")

	ctrlC := func() { _, _ = h.m.Update(keyPress("ctrl+c")) }

	ctrlC()

	if h.m.input.Value() != "" || h.m.quitting {
		t.Fatalf("the first ctrl+c: line %q, quitting %v", h.m.input.Value(), h.m.quitting)
	}

	ctrlC()

	if h.m.note != "ctrl+c again quits" || !strings.Contains(h.view(), "ctrl+c again quits") {
		t.Fatalf("ctrl+c on an empty line does not arm:\n%s", h.view())
	}

	h.clock = h.clock.Add(3 * time.Second)
	ctrlC()

	if h.m.quitting {
		t.Fatal("a ctrl+c three seconds later quits")
	}

	h.clock = h.clock.Add(time.Second)
	ctrlC()

	if !h.m.quitting {
		t.Error("a second ctrl+c within two seconds does not quit")
	}
}

// ctrl+d quits on an empty line and deletes forward otherwise.
func TestTUICtrlD(t *testing.T) {
	h := newHarness(t, tuiFake())
	h.typ("ab")
	h.press("ctrl+a", "ctrl+d")

	if h.m.input.Value() != "b" || h.quit {
		t.Fatalf("ctrl+d on \"ab\" at its start: line %q, quit %v", h.m.input.Value(), h.quit)
	}

	h.press("ctrl+e", "ctrl+u", "ctrl+d")

	if !h.quit || h.view() != "" {
		t.Errorf("ctrl+d on an empty line: quit %v, view %q", h.quit, h.view())
	}
}

// ? on an empty line shows the keys; any key closes them and does nothing
// else.
func TestTUIHelpOverlay(t *testing.T) {
	h := newHarness(t, tuiFake())
	h.press("?")

	if !strings.Contains(h.view(), "ctrl+a e u w k") {
		t.Fatalf("? shows no keys:\n%s", h.view())
	}

	h.press("x")

	if h.m.overlay || h.m.input.Value() != "" {
		t.Errorf("a key on the overlay: overlay %v, line %q", h.m.overlay, h.m.input.Value())
	}

	h.typ("a?")

	if h.m.input.Value() != "a?" || h.m.overlay {
		t.Errorf("? in a line: line %q, overlay %v", h.m.input.Value(), h.m.overlay)
	}
}

// ctrl+r fetches the level again.
func TestTUIRefresh(t *testing.T) {
	h := newHarness(t, tuiFake())
	h.press("enter", "ctrl+r")

	if h.fake.listCalls != 2 || h.m.level != levelDevices {
		t.Errorf("ctrl+r at the devices: %d List calls, level %d", h.fake.listCalls, h.m.level)
	}

	h.press("down", "enter", "ctrl+r")

	if len(h.fake.commandsCalls) != 2 {
		t.Errorf("ctrl+r at the commands: Commands calls %v", h.fake.commandsCalls)
	}
}

// An answer to a call Esc cancelled is dropped.
func TestTUIDropsACancelledAnswer(t *testing.T) {
	h := newHarness(t, tuiFake())

	h.m.Update(keyPress("enter")) // the List is not run

	if h.m.busy == nil || !strings.Contains(h.view(), "listing devices on lab:2024") {
		t.Fatalf("no call in flight:\n%s", h.view())
	}

	seq := h.m.busy.seq
	h.press("esc")
	h.update(devicesMsg{seq: seq, host: "lab:2024", visit: true})

	if h.m.level != levelHosts || h.m.busy != nil {
		t.Errorf("the cancelled answer moved the TUI: level %d", h.m.level)
	}
}

// The TUI's diagnostics go above the prompt while it draws, to the
// terminal while a run holds it, and after the TUI.
func TestSwitchWriter(t *testing.T) {
	var out bytes.Buffer

	w := newSwitchWriter(&out)
	got := make(chan tea.Msg, 2)

	go w.forward(func(msg tea.Msg) { got <- msg })

	fmt.Fprintln(w, "live")

	if msg := <-got; msg != logMsg("live") {
		t.Errorf("forwarded %v, want the live line", msg)
	}

	w.release(true)
	fmt.Fprintln(w, "released")
	w.release(false)
	w.close()
	fmt.Fprintln(w, "after")

	if out.String() != "released\nafter\n" {
		t.Errorf("written to the terminal: %q", out.String())
	}
}

// A run's outcome follows the agent's code; the hint follows the refusal.
func TestClassifyAndHint(t *testing.T) {
	tests := []struct {
		err  error
		want outcome
		hint string
	}{
		{nil, outcomeDone, ""},
		{fmt.Errorf("stream: %w", errInterrupted), outcomeInterrupted, ""},
		{connect.NewError(connect.CodeFailedPrecondition, errors.New(`device "d" is locked by "b" for 5m`)), outcomeRefused, "unlock force"},
		{connect.NewError(connect.CodeFailedPrecondition, errors.New(`device "d" is already running "c"`)), outcomeRefused, "never runs twice"},
		{connect.NewError(connect.CodeFailedPrecondition, errors.New("dutagent is shutting down")), outcomeRefused, "stopping"},
		{connect.NewError(connect.CodeNotFound, errors.New("other")), outcomeOther, ""},
		{connect.NewError(connect.CodePermissionDenied, errors.New("not yours")), outcomeOther, "another user's"},
		{connect.NewError(connect.CodeUnauthenticated, errors.New("anonymous")), outcomeUnauthenticated, "-u name@host"},
	}

	for _, tt := range tests {
		if got := classify(tt.err); got != tt.want {
			t.Errorf("classify(%v) = %d, want %d", tt.err, got, tt.want)
		}

		if hint := hintFor(tt.err); (tt.hint == "") != (hint == "") || !strings.Contains(hint, tt.hint) {
			t.Errorf("hintFor(%v) = %q, want it to contain %q", tt.err, hint, tt.hint)
		}
	}
}

// The raw banner keeps the way out at any width, and that the device owns
// every key wherever it fits; its title is bold without colour too.
func TestRawBannerFits(t *testing.T) {
	for _, width := range []int{120, 100, 80, 60, 39} {
		banner := rawBanner(false, width)
		if !strings.Contains(banner, "Ctrl-A x ends it") || !strings.HasPrefix(banner, bold) {
			t.Errorf("at %d columns the banner lost the way out or its bold: %q", width, banner)
		}

		if width >= 80 && !strings.Contains(banner, "every key goes to the device") {
			t.Errorf("at %d columns the banner lost that every key goes to the device: %q", width, banner)
		}

		if width >= 60 && ansi.StringWidth(banner) >= width {
			t.Errorf("at %d columns the banner is %d wide", width, ansi.StringWidth(banner))
		}
	}
}

// unstyled returns the text of s outside every SGR code: what shows in the
// terminal's default style, which is a module's.
func unstyled(s string) string {
	var out strings.Builder

	styled := false

	for s != "" {
		if rest, ok := strings.CutPrefix(s, "\x1b["); ok {
			var code string

			code, s, _ = strings.Cut(rest, "m")
			styled = code != "0"

			continue
		}

		r, size := utf8.DecodeRuneInString(s)
		if !styled && !unicode.IsSpace(r) {
			out.WriteRune(r)
		}

		s = s[size:]
	}

	return out.String()
}

// The TUI's transcript lines are told apart from a module's output: each
// starts with one of the TUI's glyphs, or is indented under one, and none of
// its text is in the terminal's default style.
func TestTranscriptStyles(t *testing.T) {
	fake := tuiFake()
	h := newHarness(t, fake)
	h.atCommands()
	h.typ("lock 2h")
	h.press("enter")

	fake.unlockErr = connect.NewError(connect.CodePermissionDenied, errors.New("not yours"))
	h.typ("unlock")
	h.press("enter", "enter")

	run := &runCmd{ac: agentClient{addr: "lab:2024"}, device: "demo-board", line: "power on", user: "ada@lab", width: 100, ink: true, now: time.Now}

	var console bytes.Buffer

	hook := run.announce(&crlfWriter{w: &console}, func() bool { return false })
	hook(true, true)
	hook(false, true)
	hook(true, false)
	hook(false, false)

	text := strings.Join(h.styled, "\n") + run.header() + console.String()
	for _, err := range []error{nil, errInterrupted, connect.NewError(connect.CodeFailedPrecondition, errors.New(`device "d" is locked by "b"`))} {
		run.err = err
		text += run.result()
	}

	for line := range strings.SplitSeq(text, "\n") {
		plain := ansi.Strip(line)
		if plain == "" {
			continue
		}

		if !strings.HasPrefix(plain, "  ") && !slices.ContainsFunc([]string{"●", "⎿", "▌", "✗"}, func(glyph string) bool { return strings.HasPrefix(plain, glyph) }) {
			t.Errorf("a transcript line starts with neither a glyph nor an indent: %q", plain)
		}

		if rest := unstyled(line); rest != "" {
			t.Errorf("%q of the transcript line %q is in the default style", rest, plain)
		}
	}

	run.ink = false
	if header := run.header(); !strings.Contains(header, bold+"demo-board power on") {
		t.Errorf("without colour the header is not bold: %q", header)
	}
}

// A console is bracketed by two lines in its colour, violet for raw and blue
// for line, the title bold; without colour the title stays bold.
func TestConsoleLines(t *testing.T) {
	for _, color := range []bool{true, false} {
		for raw, code := range map[bool]string{true: style.Magenta, false: style.Blue} {
			var out bytes.Buffer

			run := &runCmd{ink: ink(color), width: 100}
			hook := run.announce(&crlfWriter{w: &out}, func() bool { return false })
			hook(true, raw)
			hook(false, raw)

			title, want := lineTitle, bold
			if raw {
				title = rawTitle
			}

			if color {
				want += code
			}

			lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
			if len(lines) != 2 || ansi.Strip(lines[1]) != title+" closed" {
				t.Fatalf("raw %v, colour %v: the console lines %q", raw, color, lines)
			}

			for _, line := range lines {
				if !strings.HasPrefix(line, want+title) || color != strings.Contains(line, code) || color && unstyled(line) != "" {
					t.Errorf("raw %v, colour %v: the line %q is not in the console's style", raw, color, line)
				}
			}
		}
	}
}

func TestFormatElapsed(t *testing.T) {
	for d, want := range map[time.Duration]string{
		300 * time.Millisecond: "0.3s", 12 * time.Second: "12s", 72 * time.Second: "1m12s",
	} {
		if got := formatElapsed(d); got != want {
			t.Errorf("formatElapsed(%v) = %q, want %q", d, got, want)
		}
	}
}

// A signal ends the TUI as interrupted; a quit it asked for passes.
func TestTUISignals(t *testing.T) {
	h := newHarness(t, tuiFake())

	if got := h.m.signals(h.m, tea.InterruptMsg{}); got != (signalMsg{}) {
		t.Errorf("SIGINT became %v", got)
	}

	if got := h.m.signals(h.m, tea.QuitMsg{}); got != (signalMsg{}) {
		t.Errorf("SIGTERM became %v", got)
	}

	h.update(signalMsg{})

	if !h.quit || !errors.Is(h.m.exitErr, errInterrupted) {
		t.Errorf("a signal: quit %v with %v", h.quit, h.m.exitErr)
	}

	if got := h.m.signals(h.m, tea.QuitMsg{}); got != (tea.QuitMsg{}) {
		t.Errorf("the TUI's own quit became %v", got)
	}
}
