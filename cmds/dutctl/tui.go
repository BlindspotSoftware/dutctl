// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/buildinfo"
	"github.com/BlindspotSoftware/dutctl/internal/keyword"
	"github.com/BlindspotSoftware/dutctl/internal/output"
	"github.com/BlindspotSoftware/dutctl/internal/rpc"
	"github.com/BlindspotSoftware/dutctl/internal/style"
	"github.com/charmbracelet/colorprofile"

	tea "charm.land/bubbletea/v2"
)

// level is where the TUI's prompt is: the depth of its breadcrumb.
type level int

const (
	levelHosts level = iota
	levelDevices
	levelCommands
)

// Limits of the TUI.
const (
	quitArmWindow    = 2 * time.Second // a second ctrl+c within it quits
	recentDevicesMax = 5
	narrowWidth      = 70 // below it the right-hand columns go
	bareWidth        = 40 // below it the prompt loses its box
	startWidth       = 80 // until the terminal tells its size
	startHeight      = 24
)

// runTUI runs the TUI on the terminal until the user quits. It
// returns nil then, errInterrupted when SIGTERM, SIGHUP or an outside SIGINT
// ended it, and an error when the TUI could not run.
//
// The TUI's own context ends with SIGTERM and SIGHUP only: Ctrl-C ending a
// run in a line console must not end the session.
func (app *application) runTUI() error {
	tuiCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	if os.Getenv("NO_COLOR") != "" && !app.noColor {
		app.noColor = true
		app.formatter = output.New(output.Config{
			Stdout:  app.console.outText,
			Stderr:  app.console.errText,
			Format:  app.outputFormat,
			Verbose: app.verbose,
			NoColor: true,
		})
	}

	// Straight to the terminal before the program starts, so it comes first
	// whatever the renderer does.
	app.splash(app.console.outText)

	// Diagnostics must not write over the live view: they go above it, or
	// straight to the terminal while a run holds it.
	diag := newSwitchWriter(app.console.errText)
	defer diag.close()

	app.logHandler = newCLIHandler(diag, app.logHandler.mode, !app.noColor && app.console.errTTY)
	app.logHandler.muted = true
	slog.SetDefault(slog.New(app.logHandler))

	tui := newModel(tuiCtx, app, diag)

	opts := []tea.ProgramOption{
		tea.WithInput(app.console.stdin),
		tea.WithOutput(app.console.stdout),
		tea.WithFilter(tui.signals),
	}
	if app.noColor {
		opts = append(opts, tea.WithColorProfile(colorprofile.ASCII))
	}

	program := tea.NewProgram(tui, opts...)
	go diag.forward(program.Send)

	// The program's own context would kill it, leaving its frame behind;
	// a message lets the TUI quit as on ctrl+d.
	go func() {
		<-tuiCtx.Done()
		program.Send(signalMsg{})
	}()

	_, err := program.Run()

	diag.close()

	return tuiExit(err, tui.exitErr)
}

// signals turns bubbletea's answer to SIGINT and SIGTERM into a quit the
// TUI sees: it erases its frame and exits as interrupted. A quit the TUI
// asked for passes. It runs on the event loop, as Update does.
func (m *model) signals(_ tea.Model, msg tea.Msg) tea.Msg {
	switch msg.(type) {
	case tea.InterruptMsg:
		return signalMsg{}
	case tea.QuitMsg:
		if !m.quitting {
			return signalMsg{}
		}
	}

	return msg
}

// tuiExit maps how the program ended to the client's exit: a signal is an
// interrupt (exit 130), a panic or a terminal that cannot be used an error
// (exit 1).
func tuiExit(runErr, tuiErr error) error {
	switch {
	case tuiErr != nil:
		return tuiErr
	case runErr == nil:
		return nil
	case errors.Is(runErr, tea.ErrProgramPanic):
		return runErr
	case errors.Is(runErr, tea.ErrInterrupted), errors.Is(runErr, tea.ErrProgramKilled):
		return errInterrupted
	default:
		return runErr
	}
}

// fetch is the unary call the TUI waits for, with a spinner; Esc cancels
// it.
type fetch struct {
	seq    int
	label  string
	cancel context.CancelFunc
}

// model is the TUI. Bubbletea calls its methods on one goroutine; the
// commands it returns run on others and capture values only, never the
// model.
type model struct {
	app      *application
	newAgent func(addr string) agentClient
	clients  map[string]agentClient // per host, so a connection is reused
	fetchCtx func() (context.Context, context.CancelFunc)
	runCtx   func() (context.Context, context.CancelFunc)
	tuiDone  <-chan struct{}
	diag     *switchWriter
	now      func() time.Time

	user  string
	cwd   string
	ink   ink // colour, of the transcript and of the live view
	dark  bool
	theme theme

	width, height int

	level    level
	host     string
	device   string
	devices  []output.DeviceEntry // of the host, for the lock states
	commands []string
	help     map[string]string // per command of the device, as it arrives
	hist     history
	// unreachable marks the hosts the last call could not reach.
	unreachable map[string]bool

	input textinput.Model
	menu  menu
	spin  spinner.Model
	seq   int
	busy  *fetch
	// pending is the rest of a typed line, run once the level it belongs to
	// is reached.
	pending []string
	// typedAhead holds the keys typed while busy, acted on once the answer
	// is in, so a line typed ahead is never cut by the call it waited for.
	typedAhead []tea.KeyPressMsg
	running    bool
	run        *runCmd // the run in progress, or the last one
	overlay    bool
	quitArm    time.Time
	note       string // replaces the first key hint: "enter retries", "ctrl+c again quits"
	quitting   bool
	exitErr    error
	// framed tells that bubbletea drew its first frame; backlog holds what
	// was printed before.
	framed  bool
	backlog []string
}

func newModel(tuiCtx context.Context, app *application, diag *switchWriter) *model {
	cwd, _ := os.Getwd()

	tui := &model{
		app:      app,
		newAgent: func(addr string) agentClient { return newAgentClient(addr, app.user) },
		clients:  map[string]agentClient{app.serverAddr: app.agent()},
		fetchCtx: func() (context.Context, context.CancelFunc) { return context.WithCancel(tuiCtx) },
		runCtx:   func() (context.Context, context.CancelFunc) { return signal.NotifyContext(tuiCtx, os.Interrupt) },
		tuiDone:  tuiCtx.Done(),
		diag:     diag,
		now:      time.Now,
		user:     app.user,
		cwd:      shortPath(cwd, os.Getenv("HOME")),
		ink:      ink(!app.noColor),
		dark:     true,
		width:    startWidth,
		height:   startHeight,
		hist:     readHistory(historyPath()),
		help:     map[string]string{},

		unreachable: map[string]bool{},
		spin:        spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		menu:        menu{prefer: rowRecentDevice},
	}
	tui.restyle()

	return tui
}

// shortPath abbreviates the home directory in path to ~.
func shortPath(path, home string) string {
	if home != "" && (path == home || strings.HasPrefix(path, home+"/")) {
		return "~" + strings.TrimPrefix(path, home)
	}

	return path
}

// restyle rebuilds the styles for the background.
func (m *model) restyle() {
	m.theme = newTheme(m.dark, bool(m.ink))
	value := m.input.Value()
	m.input = newInput(m.theme)
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.spin.Style = m.theme.accent
	m.fitInput()
}

// Init prints the TUI's greeting, asks the terminal for its background, and
// connects at once when -s was given.
func (m *model) Init() tea.Cmd {
	greeting := strong("dutctl") + m.ink.paint(style.Gray, fmt.Sprintf(" %s · TUI · %s · %s\n%s",
		buildinfo.Version, m.user, m.cwd, "? shows the keys · esc goes up · ctrl+d quits"))

	cmds := []tea.Cmd{
		m.print(greeting),
		tea.RequestBackgroundColor,
		tea.Tick(firstFrameWait, func(time.Time) tea.Msg { return framedMsg{} }),
	}
	if m.app.serverGiven {
		cmds = append(cmds, m.connect(m.app.serverAddr, nil))
	}

	return tea.Batch(cmds...)
}

// firstFrameWait bounds the wait for the terminal's answer about its
// background, which tells that the first frame is out.
const firstFrameWait = 150 * time.Millisecond

// print prints text above the prompt, into the terminal's scrollback. Until
// the first frame is out, it is held back: bubbletea would insert it at the
// top of the screen, over what the terminal showed.
func (m *model) print(text string) tea.Cmd {
	if !m.framed {
		m.backlog = append(m.backlog, text)

		return nil
	}

	return tea.Println(text)
}

// unblock prints what print held back: the first frame is out.
func (m *model) unblock() tea.Cmd {
	if m.framed {
		return nil
	}

	m.framed = true
	if len(m.backlog) == 0 {
		return nil
	}

	text := strings.Join(m.backlog, "\n")
	m.backlog = nil

	return tea.Println(text)
}

// Message types of the TUI's calls and runs.
type (
	// devicesMsg is the answer to List on entering a host (visit) or on a
	// refresh.
	devicesMsg struct {
		seq     int
		host    string
		entries []output.DeviceEntry
		visit   bool
		err     error
	}
	// commandsMsg is the answer to Commands on entering a device or on a
	// refresh.
	commandsMsg struct {
		seq    int
		host   string
		device string
		names  []string
		visit  bool
		err    error
	}
	// detailsMsg is one command's help, fetched for its description.
	detailsMsg struct {
		host, device, command, text string
		err                         error
	}
	// statusMsg is a List refreshed silently for the lock states.
	statusMsg struct {
		host    string
		entries []output.DeviceEntry
		err     error
	}
	// unaryMsg is the answer to lock, unlock or help.
	unaryMsg struct {
		seq   int
		line  string
		entry output.DeviceEntry
		kind  string // keyword.Lock, keyword.Unlock or keyword.Help
		text  string // the help
		err   error
	}
	// runDoneMsg ends a run; err is set when the terminal could not be taken
	// back.
	runDoneMsg struct {
		cmd *runCmd
		err error
	}
	// historyMsg is the history read again.
	historyMsg history
	// signalMsg tells that a signal ends the TUI.
	signalMsg struct{}
	// framedMsg ends the wait for the first frame.
	framedMsg struct{}
	// disarmMsg ends the window for the second ctrl+c.
	disarmMsg struct{ at time.Time }
	// logMsg is a diagnostic line to print above the prompt.
	logMsg string
)

// Update handles a message. Bubbletea's Model interface fixes its signature.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		cmd = m.handleKey(msg)
	case tea.PasteMsg:
		if m.busy == nil && !m.running {
			cmd = m.edit(msg)
		}
	case spinner.TickMsg:
		if m.busy != nil {
			m.spin, cmd = m.spin.Update(msg)
		}
	default:
		cmd = m.handleEvent(msg)
	}

	cmd = tea.Batch(cmd, m.replay())
	m.fitInput()

	return m, cmd
}

// replay acts on the keys typed while the TUI waited for an answer, once
// it is in: a key that starts another call queues the rest again, and a run
// drops them, as it drops the keys typed during it.
func (m *model) replay() tea.Cmd {
	if m.busy != nil || len(m.typedAhead) == 0 {
		return nil
	}

	keys := m.typedAhead
	m.typedAhead = nil

	cmds := make([]tea.Cmd, 0, len(keys))
	for _, key := range keys {
		cmds = append(cmds, m.handleKey(key))
	}

	return tea.Batch(cmds...)
}

// handleEvent handles what the terminal and the TUI's timers tell.
func (m *model) handleEvent(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		m.restyle()

		return m.unblock()
	case framedMsg:
		return m.unblock()
	case signalMsg:
		m.exitErr = errInterrupted

		return m.quit()
	case disarmMsg:
		if msg.at.Equal(m.quitArm) {
			m.quitArm, m.note = time.Time{}, ""
		}
	case logMsg:
		return m.print(string(msg))
	default:
		return m.handleResult(msg)
	}

	return nil
}

// handleResult handles the answer of a call or the end of a run.
func (m *model) handleResult(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case devicesMsg:
		return m.onDevices(msg)
	case commandsMsg:
		return m.onCommands(msg)
	case unaryMsg:
		return m.onUnary(msg)
	case runDoneMsg:
		return m.onRunDone(msg)
	default:
		m.absorb(msg)
	}

	return nil
}

// absorb takes in what arrives in the background: help texts, lock states,
// the history read again.
func (m *model) absorb(msg tea.Msg) {
	switch msg := msg.(type) {
	case detailsMsg:
		if msg.host == m.host && msg.device == m.device && msg.err == nil {
			m.help[msg.command] = msg.text
		}
	case statusMsg:
		if msg.host == m.host && msg.err == nil {
			m.devices = msg.entries
		}
	case historyMsg:
		m.hist = history(msg)
	}
}

// handleKey handles a key at the prompt. A key read before a run took the
// terminal, a second Enter in the same read, does nothing: it must not act on
// the line just run.
func (m *model) handleKey(key tea.KeyPressMsg) tea.Cmd {
	if m.running {
		return nil
	}

	if m.overlay {
		m.overlay = false

		return nil
	}

	switch key.String() {
	case "ctrl+c":
		return m.interrupt()
	case "ctrl+d":
		if m.input.Value() == "" {
			return m.quit()
		}
	case "ctrl+l":
		return tea.ClearScreen
	}

	if m.busy != nil {
		if key.String() == keyEsc {
			m.cancelFetch()
		} else {
			m.typedAhead = append(m.typedAhead, key)
		}

		return nil
	}

	if cmd, handled := m.navigate(key.String()); handled {
		return cmd
	}

	return m.edit(key)
}

// navigate handles the keys that act on the menu and the levels; it reports
// whether key was one of them.
func (m *model) navigate(key string) (tea.Cmd, bool) {
	_, entry := m.argEntry()

	switch key {
	case "enter":
		return m.enter(), true
	case "tab":
		return m.tab(), true
	case keyEsc:
		m.escape()

		return nil, true
	case "ctrl+r":
		return m.refresh(), true
	case "up", "ctrl+p", "down", "ctrl+n", "pgup", "pgdown":
		switch {
		case m.menu.folded: // the first press only unfolds
			m.menu.folded = false
		case !entry:
			m.menu.move(m.rows(), m.moveBy(key))
		}

		return nil, true
	case "?":
		if m.input.Value() != "" {
			return nil, false
		}

		m.overlay = true

		return nil, true
	}

	return nil, false
}

// moveBy is how far key moves the highlight.
func (m *model) moveBy(key string) int {
	switch key {
	case "up", "ctrl+p":
		return -1
	case "pgup":
		return -m.maxMenuRows()
	case "pgdown":
		return m.maxMenuRows()
	default:
		return 1
	}
}

// edit hands a key or a paste to the line editor.
func (m *model) edit(msg tea.Msg) tea.Cmd {
	before := m.input.Value()

	var cmd tea.Cmd

	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		m.lineChanged()
	}

	return cmd
}

// interrupt handles ctrl+c: it cancels a call, else clears the line, else
// arms; a second press within quitArmWindow quits.
func (m *model) interrupt() tea.Cmd {
	switch {
	case m.busy != nil:
		m.cancelFetch()

		return nil
	case m.input.Value() != "":
		m.clearLine()

		return nil
	}

	now := m.now()
	if !m.quitArm.IsZero() && now.Sub(m.quitArm) < quitArmWindow {
		return m.quit()
	}

	m.quitArm, m.note = now, "ctrl+c again quits"

	return tea.Tick(quitArmWindow, func(time.Time) tea.Msg { return disarmMsg{at: now} })
}

// quit ends the TUI; the live view is erased, the transcript stays.
func (m *model) quit() tea.Cmd {
	m.quitting = true

	return tea.Quit
}

// keyEsc is the name of the Escape key.
const keyEsc = "esc"

// escape clears the line, else goes one level up, the highlight on where the
// prompt was.
func (m *model) escape() {
	if m.input.Value() != "" {
		m.clearLine()

		return
	}

	switch m.level {
	case levelCommands:
		m.level = levelDevices
		m.menu = menu{prefer: rowDevice}
		m.menu.choose(rowDevice, m.device)
		m.device = ""
	case levelDevices:
		m.level = levelHosts
		m.menu = menu{prefer: rowRecentDevice}
		m.menu.choose(rowHost, m.host)
		m.host = ""
	case levelHosts:
	}
}

// clearLine empties the line.
func (m *model) clearLine() {
	m.input.SetValue("")
	m.lineChanged()
}

// refresh fetches the level again: the history, the devices, or the
// commands with their help and the lock states.
func (m *model) refresh() tea.Cmd {
	switch m.level {
	case levelDevices:
		return m.fetchDevices(m.host, false)
	case levelCommands:
		return m.fetchCommands(m.device, false)
	default:
		path := m.hist.path

		return func() tea.Msg { return historyMsg(readHistory(path)) }
	}
}

// client returns the agent client for addr, made once per host.
func (m *model) client(addr string) agentClient {
	if ac, ok := m.clients[addr]; ok {
		return ac
	}

	ac := m.newAgent(addr)
	m.clients[addr] = ac

	return ac
}

// startFetch starts a call the TUI waits for, with the spinner; call gets
// the call's context and sequence number.
func (m *model) startFetch(label string, call func(ctx context.Context, seq int) tea.Msg) tea.Cmd {
	m.seq++
	seq := m.seq
	ctx, cancel := m.fetchCtx()
	m.busy = &fetch{seq: seq, label: label, cancel: cancel}

	return tea.Batch(m.spin.Tick, func() tea.Msg {
		defer cancel()

		return call(ctx, seq)
	})
}

// cancelFetch cancels the call the TUI waits for; its answer is dropped,
// and so are the keys typed while it was waited for.
func (m *model) cancelFetch() {
	m.busy.cancel()
	m.busy = nil
	m.pending = nil
	m.typedAhead = nil
}

// current reports whether a call's answer is the one the TUI waits for,
// and stops waiting if so.
func (m *model) current(seq int) bool {
	if m.busy == nil || m.busy.seq != seq {
		return false
	}

	m.busy = nil

	return true
}

// connect enters the host at addr; pending is what follows it in a typed
// line, a device first.
func (m *model) connect(addr string, pending []string) tea.Cmd {
	host, err := rpc.DialAddr(addr)
	if err != nil {
		return m.printFailure(err.Error())
	}

	m.pending = pending

	return m.fetchDevices(host, true)
}

// fetchDevices lists the devices of host; on a visit, it enters the host.
func (m *model) fetchDevices(host string, visit bool) tea.Cmd {
	ac := m.client(host)

	return m.startFetch("listing devices on "+host, func(ctx context.Context, seq int) tea.Msg {
		entries, err := ac.listDevices(ctx)

		return devicesMsg{seq: seq, host: host, entries: entries, visit: visit, err: err}
	})
}

func (m *model) onDevices(msg devicesMsg) tea.Cmd {
	if !m.current(msg.seq) {
		return nil
	}

	if msg.err != nil {
		return m.fetchFailed(msg.host, msg.err)
	}

	delete(m.unreachable, msg.host)
	m.devices = msg.entries

	if !msg.visit {
		return nil
	}

	m.level, m.host, m.device = levelDevices, msg.host, ""
	m.menu = menu{prefer: rowDevice}
	m.clearLine()

	count := fmt.Sprintf("%d devices", len(msg.entries))
	if len(msg.entries) == 1 {
		count = "1 device"
	}

	cmds := []tea.Cmd{
		m.print("\n" + m.ink.paint(style.Cyan, "●") + " " + strong(msg.host) + m.ink.paint(style.Gray, " · "+count)),
		m.record(histEntry{at: m.now(), host: msg.host}),
	}

	if len(m.pending) > 0 {
		device, rest := m.pending[0], m.pending[1:]
		cmds = append(cmds, m.openDevice(device, rest))
	}

	return tea.Sequence(cmds...)
}

// openDevice enters device of the current host; pending is a line to run
// there.
func (m *model) openDevice(device string, pending []string) tea.Cmd {
	m.pending = pending

	return m.fetchCommands(device, true)
}

// fetchCommands lists the commands of device; on a visit, it enters the
// device.
func (m *model) fetchCommands(device string, visit bool) tea.Cmd {
	host, ac := m.host, m.client(m.host)

	return m.startFetch("listing commands of "+device, func(ctx context.Context, seq int) tea.Msg {
		names, err := ac.deviceCommands(ctx, device)

		return commandsMsg{seq: seq, host: host, device: device, names: names, visit: visit, err: err}
	})
}

// onCommands enters the device, or refreshes it: its commands at once, their
// help as it arrives, and its lock state. A pending line runs now.
func (m *model) onCommands(msg commandsMsg) tea.Cmd {
	if !m.current(msg.seq) {
		return nil
	}

	if msg.err != nil {
		return m.fetchFailed(msg.host, msg.err)
	}

	m.level, m.device, m.commands = levelCommands, msg.device, msg.names
	cmds := []tea.Cmd{m.fetchStatus()}

	if msg.visit {
		m.help = map[string]string{}
		m.menu = menu{prefer: rowCommand}
		m.clearLine()
		cmds = append(cmds, m.record(histEntry{at: m.now(), host: msg.host, device: msg.device}))
	}

	ac := m.client(msg.host)
	for _, name := range msg.names {
		cmds = append(cmds, fetchDetails(m.fetchCtx, ac, msg.host, msg.device, name))
	}

	if len(m.pending) > 0 {
		line := strings.Join(m.pending, " ")
		m.pending = nil

		cmds = append(cmds, m.runLine(line))
	}

	return tea.Batch(cmds...)
}

// fetchDetails fetches the help of a command, for its description.
func fetchDetails(newCtx func() (context.Context, context.CancelFunc), ac agentClient, host, device, command string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := newCtx()
		defer cancel()

		text, err := ac.commandDetails(ctx, device, command, keyword.Help)

		return detailsMsg{host: host, device: device, command: command, text: text, err: err}
	}
}

// fetchStatus lists the devices of the current host silently, for the lock
// states.
func (m *model) fetchStatus() tea.Cmd {
	host, ac, newCtx := m.host, m.client(m.host), m.fetchCtx

	return func() tea.Msg {
		ctx, cancel := newCtx()
		defer cancel()

		entries, err := ac.listDevices(ctx)

		return statusMsg{host: host, entries: entries, err: err}
	}
}

// fetchFailed shows why a call failed. An unreachable agent sends the prompt
// to the hosts, the host marked; any other failure keeps the level. A line
// pending is dropped, and so are the keys typed while the call was waited for.
func (m *model) fetchFailed(host string, err error) tea.Cmd {
	m.pending, m.typedAhead = nil, nil

	if connect.CodeOf(err) == connect.CodeUnavailable {
		m.toHosts(host)
	}

	return m.printFailure(userFacingError(err, host))
}

// toHosts goes to the hosts level, with host marked as unreachable and
// highlighted.
func (m *model) toHosts(host string) {
	m.unreachable[host] = true
	m.level, m.host, m.device = levelHosts, "", ""
	m.menu = menu{prefer: rowRecentDevice}
	m.menu.choose(rowHost, host)
	m.clearLine()
}

// printFailure prints a failure above the prompt.
func (m *model) printFailure(text string) tea.Cmd {
	return m.print(m.ink.paint(style.Red, style.MarkerError+" "+text))
}

// startLock reserves the current device.
func (m *model) startLock(line string, duration time.Duration) tea.Cmd {
	device, ac := m.device, m.client(m.host)

	return m.startFetch("locking "+device, func(ctx context.Context, seq int) tea.Msg {
		entry, err := ac.lockDevice(ctx, device, duration)

		return unaryMsg{seq: seq, line: line, kind: keyword.Lock, entry: entry, err: err}
	})
}

// startUnlock releases the current device.
func (m *model) startUnlock(line string, force bool) tea.Cmd {
	device, ac := m.device, m.client(m.host)

	return m.startFetch("unlocking "+device, func(ctx context.Context, seq int) tea.Msg {
		err := ac.unlockDevice(ctx, device, force)

		return unaryMsg{seq: seq, line: line, kind: keyword.Unlock, err: err}
	})
}

// showHelp fetches a command's help to print it.
func (m *model) showHelp(line, command string) tea.Cmd {
	device, ac := m.device, m.client(m.host)

	return m.startFetch("fetching the help of "+command, func(ctx context.Context, seq int) tea.Msg {
		text, err := ac.commandDetails(ctx, device, command, keyword.Help)

		return unaryMsg{seq: seq, line: line, kind: keyword.Help, text: text, err: err}
	})
}

// onUnary prints the answer to lock, unlock or help in the transcript and
// folds the menu, so the answer stays in sight; after help the highlight is on
// the command, as after a run. It never moves the level; the lock state is
// fetched again.
func (m *model) onUnary(msg unaryMsg) tea.Cmd {
	if !m.current(msg.seq) {
		return nil
	}

	gray := func(s string) string { return m.ink.paint(style.Gray, s) }
	out := "\n" + m.ink.paint(style.Cyan, "●") + " " + strong(m.device+" "+msg.line) + "\n"

	switch {
	case msg.err != nil:
		m.typedAhead = nil // typed for a device the call left as it was

		out += gray("⎿ ") + m.ink.paint(style.Red, style.MarkerError+" "+userFacingError(msg.err, m.host))
		if hint := hintFor(msg.err); hint != "" {
			out += "\n  " + gray(hint)
		}
	case msg.kind == keyword.Help:
		out += strings.TrimRight(msg.text, "\n")
	case msg.kind == keyword.Lock:
		text, _ := m.lockNote(msg.entry)
		out += gray("⎿ ") + m.ink.paint(style.Green, style.MarkerSuccess+" "+text)
	default:
		out += gray("⎿ ") + m.ink.paint(style.Green, style.MarkerSuccess+" unlocked")
	}

	m.clearLine()

	if msg.kind == keyword.Help {
		command, _, _ := strings.Cut(msg.line, " ")
		m.menu.choose(rowCommand, command)
	}

	m.menu.folded = true

	return tea.Batch(m.print(out), m.fetchStatus())
}

// lockNote renders a device's lock state as the formatter words it, the
// user's own as "you": a hold of the user's own reads as success, another's
// reservation as a warning, another's running command as context.
func (m *model) lockNote(entry output.DeviceEntry) (string, tone) {
	if !entry.Locked {
		return "", toneMuted
	}

	text := strings.Trim(strings.TrimSpace(output.LockAnnotation(entry)), "[]")

	switch {
	case entry.Owner == m.user:
		return strings.Replace(text, fmt.Sprintf("by %q", entry.Owner), "by you", 1), toneSuccess
	case entry.ExpiresAt > 0:
		return text, toneWarning
	default:
		return text, toneMuted
	}
}

// startRun hands the terminal to a run of command on the current device.
func (m *model) startRun(line, command string, args []string) tea.Cmd {
	run := &runCmd{
		app:     m.app,
		ac:      m.client(m.host),
		device:  m.device,
		command: command,
		args:    args,
		line:    line,
		width:   m.width,
		user:    m.user,
		ink:     m.ink,
		runCtx:  m.runCtx,
		diag:    m.diag,
		now:     m.now,
	}

	m.running, m.run = true, run
	m.clearLine() // the landing puts it back where retrying it is the fix

	return tea.Exec(run, func(err error) tea.Msg { return runDoneMsg{cmd: run, err: err} })
}

// onRunDone takes the terminal back after a run: the line goes into the
// history if the agent took it, and the prompt lands where the next step is.
func (m *model) onRunDone(msg runDoneMsg) tea.Cmd {
	m.running = false

	if msg.err != nil {
		m.exitErr = fmt.Errorf("taking the terminal back: %w", msg.err)

		return m.quit()
	}

	run := msg.cmd
	result := classify(run.err)

	if result == outcomeInterrupted && closed(m.tuiDone) {
		m.exitErr = errInterrupted

		return m.quit()
	}

	var cmds []tea.Cmd

	if result.recorded() {
		cmds = append(cmds, m.record(histEntry{at: m.now(), host: run.ac.addr, device: run.device, line: run.line, raw: run.raw.Load()}))
	}

	return tea.Batch(append(cmds, m.land(result, run))...)
}

// land applies the landing rule: success lands where the run started, the
// highlight on the command run; a failure lands at the level that can fix
// it, and the line is kept only when editing or retrying it is the fix. On
// the device the menu folds, so the run's output stays in sight, unless the
// help is the fix or the commands changed.
func (m *model) land(result outcome, run *runCmd) tea.Cmd {
	m.clearLine()
	m.menu = menu{prefer: rowCommand}
	m.menu.choose(rowCommand, run.command)

	switch result {
	case outcomeBadArgs:
		m.keepLine(run)

		return nil
	case outcomeUnauthenticated:
		m.menu.folded = true

		return nil
	case outcomeRefused:
		m.keepLine(run)
		m.note = "enter retries"
	case outcomeNoCommand:
		m.menu = menu{prefer: rowCommand}

		return m.fetchCommands(m.device, false)
	case outcomeNoDevice:
		m.level = levelDevices
		m.menu = menu{prefer: rowDevice}
		m.device = ""

		return m.fetchDevices(m.host, false)
	case outcomeUnreachable:
		m.toHosts(run.ac.addr)

		return nil
	case outcomeDone, outcomeInterrupted, outcomeAborted, outcomeOther:
	}

	m.menu.folded = true

	return m.fetchStatus()
}

// keepLine puts the line of run back, for editing or retrying.
func (m *model) keepLine(run *runCmd) {
	line := run.line
	if len(run.args) == 0 {
		line += " "
	}

	m.setLine(line)
}

// closed reports whether done is closed.
func closed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// record remembers e: in memory at once, in the file by a command.
func (m *model) record(entry histEntry) tea.Cmd {
	rewrite := m.hist.add(entry)

	path := m.hist.path
	if path == "" {
		return nil
	}

	var all []histEntry
	if rewrite {
		all = slices.Clone(m.hist.entries)
	}

	return func() tea.Msg {
		err := writeHistory(path, entry, all)
		if err != nil {
			slog.Debug("cannot write the TUI's history", "path", path, "err", err)
		}

		return nil
	}
}

// rows builds the rows of the current level.
func (m *model) rows() []row {
	switch m.level {
	case levelDevices:
		return m.deviceRows()
	case levelCommands:
		return m.commandRows()
	default:
		return m.hostRows()
	}
}

// hostRows are the start: the recent devices, then the hosts, the -s host
// always among them.
func (m *model) hostRows() []row {
	now := m.now()

	var rows []row

	if recent := m.hist.recentDevices(recentDevicesMax); len(recent) > 0 {
		rows = append(rows, row{kind: rowHeader, name: "Recent devices"})

		for _, entry := range recent {
			note := ago(entry.at, now)
			if entry.line != "" {
				note += " · " + entry.line
			}

			rows = append(rows, row{kind: rowRecentDevice, name: entry.device, host: entry.host, desc: entry.host, note: note, raw: entry.raw})
		}
	}

	rows = append(rows, row{kind: rowHeader, name: "Hosts"})

	listed := false

	for _, entry := range m.hist.hosts() {
		listed = listed || entry.host == m.app.serverAddr
		rows = append(rows, m.hostRow(entry.host, ago(entry.at, now)))
	}

	if !listed {
		note := "default · never"
		if m.app.serverGiven {
			note = "-s · never"
		}

		rows = append(rows, m.hostRow(m.app.serverAddr, note))
	}

	return rows
}

// hostRow is a host's row; one the last call could not reach says so.
func (m *model) hostRow(host, note string) row {
	if m.unreachable[host] {
		return row{kind: rowHost, name: host, note: "unreachable · enter retries", glyph: "✗", tone: toneError}
	}

	return row{kind: rowHost, name: host, note: note}
}

// deviceRows are a host's devices with their lock states.
func (m *model) deviceRows() []row {
	rows := make([]row, 0, len(m.devices))

	for _, entry := range m.devices {
		note, meaning := m.lockNote(entry)

		glyph := ""
		if entry.Locked {
			glyph = "○"
			if entry.ExpiresAt == 0 {
				glyph = "●"
			}
		}

		rows = append(rows, row{kind: rowDevice, name: entry.Name, note: note, glyph: glyph, tone: meaning})
	}

	return rows
}

// commandRows are a device's commands with their first help line, then the
// keywords, which are picked like commands; while arguments are typed, the
// command's row alone.
func (m *model) commandRows() []row {
	if name, entry := m.argEntry(); entry {
		return []row{m.commandRow(name)}
	}

	rows := []row{{kind: rowHeader, name: "Commands"}}
	for _, name := range m.commands {
		rows = append(rows, m.commandRow(name))
	}

	return append(rows, row{kind: rowHeader, name: "Device"}, m.commandRow(keyword.Lock), m.commandRow(keyword.Unlock))
}

// commandRow is a command's row, described by its help's first line; a
// keyword's describes the keyword.
func (m *model) commandRow(name string) row {
	switch name {
	case keyword.Lock:
		return row{kind: rowCommand, name: name, desc: "reserve the device for you: lock [duration], e.g. lock 2h"}
	case keyword.Unlock:
		return row{kind: rowCommand, name: name, desc: "release your reservation; unlock force releases another's"}
	}

	return row{kind: rowCommand, name: name, desc: describe(m.help[name])}
}

// describe returns the first line of a command's help, the generated
// "Command with N module(s): a, b" shortened to "N modules: a, b".
func describe(help string) string {
	first := ""

	for line := range strings.SplitSeq(help, "\n") {
		if first = strings.TrimSpace(line); first != "" {
			break
		}
	}

	rest, generated := strings.CutPrefix(first, "Command with ")
	if !generated {
		return first
	}

	count, modules, ok := strings.Cut(rest, " module(s): ")
	if !ok {
		return first
	}

	if count == "1" {
		return "1 module: " + modules
	}

	return count + " modules: " + modules
}
