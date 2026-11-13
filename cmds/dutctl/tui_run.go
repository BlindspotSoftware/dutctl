// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/style"
	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"
)

// bold is the SGR code for bold text, combined with a colour by ink.
const bold = "\033[1m"

// ink colours the lines the TUI leaves in the terminal's scrollback, the
// transcript, with the same ANSI-16 codes as the formatter, so they read as one
// with the run's output on any terminal. It is off without colour.
//
// The transcript is told apart from a module's output, which the TUI leaves
// as it is: a line of the TUI's starts in column 0 with one of its glyphs,
// ● ⎿ ▌ ✗, or the log handler's ⚠, or is indented under one, and none of its
// text is in the terminal's default style. Without colour, bold and the
// glyphs remain.
type ink bool

func (i ink) paint(code, s string) string {
	return style.Colorize(bool(i), code, s)
}

// strong renders s bold, also without colour.
func strong(s string) string {
	return style.Colorize(true, bold, s)
}

// runCmd is one run in the TUI: bubbletea hands it the terminal through
// tea.Exec, and it drives the command line's run path on it, framed by the
// transcript's header and result lines. The outcome stays in the struct for
// the landing, never in Run's error.
type runCmd struct {
	app             *application
	ac              agentClient
	device, command string
	args            []string
	line            string // the line as run, relative to the device
	width           int    // the terminal's, at the start of the run
	user            string
	ink             ink
	runCtx          func() (context.Context, context.CancelFunc)
	diag            *switchWriter
	now             func() time.Time

	elapsed time.Duration
	raw     atomic.Bool // a raw console opened; set by the receive routine
	// lineEnded tells that the interrupt closed a console, whose close
	// started its line afresh.
	lineEnded atomic.Bool
	err       error
}

// The streams are those of the console, which bubbletea's are anyway.
func (r *runCmd) SetStdin(io.Reader)  {}
func (r *runCmd) SetStdout(io.Writer) {}
func (r *runCmd) SetStderr(io.Writer) {}

// Run runs the command while bubbletea has released the terminal: cooked, no
// reader of its own, the renderer stopped. Everything it prints goes through
// the console's writers, so it lands in order with the run's output and at
// column 0 while raw. Keys typed during the run and not taken by a console
// are dropped before the prompt returns, so an Enter typed during a slow run
// cannot act on the menu.
func (r *runCmd) Run() error {
	r.diag.release(true)
	defer r.diag.release(false)

	ctx, stop := r.runCtx()
	defer stop()

	con := r.app.console.forRun()
	con.onConsole = r.announce(con.outText, func() bool { return ctx.Err() != nil })

	fmt.Fprint(con.outText, r.header())

	started := r.now()
	r.err = asInterrupt(ctx, r.app.runWith(ctx, con, r.ac, r.device, r.command, r.args))
	r.elapsed = r.now().Sub(started)

	r.app.formatter.Flush()

	if errors.Is(r.err, errInterrupted) && con.tty && !r.lineEnded.Load() {
		fmt.Fprint(con.outText, "\n") // after the ^C a cooked terminal echoed
	}

	con.outText.endLine()
	r.app.logHandler.Flush() // the run's warnings, on lines of their own
	fmt.Fprint(con.outText, r.result())

	if con.tty {
		discardInput(con.fd)
	}

	return nil
}

// header is the transcript's line before the run: the device and the line, and
// where, who and when, right-aligned, or on a line of its own when the terminal
// is too narrow.
func (r *runCmd) header() string {
	left := r.ink.paint(style.Cyan, "●") + " " + strong(r.device+" "+r.line)
	tail := r.ac.addr + " · " + r.user + " · " + r.now().Format("15:04:05")

	space := r.width - margin - ansi.StringWidth(left) - ansi.StringWidth(tail)
	if space < gap {
		return "\n" + left + "\n" + pad(indent) + r.ink.paint(style.Gray, tail) + "\n"
	}

	return "\n" + left + pad(space) + r.ink.paint(style.Gray, tail) + "\n"
}

// result is the transcript's line after the run: how it ended and how long
// it took, and for a refusal a hint at what to do.
func (r *runCmd) result() string {
	facts := " · " + formatElapsed(r.elapsed)
	if r.raw.Load() {
		facts += " · raw console"
	}

	var status string

	switch {
	case r.err == nil:
		status = r.ink.paint(style.Green, "done") + r.ink.paint(style.Gray, facts)
	case errors.Is(r.err, errInterrupted):
		status = r.ink.paint(style.Yellow, "interrupted") + r.ink.paint(style.Gray, facts)
	default:
		why := userFacingError(r.err, r.ac.addr)
		status = r.ink.paint(style.Red, "failed") + r.ink.paint(style.Gray, facts+" · ") + r.ink.paint(style.Red, why)
	}

	out := r.ink.paint(style.Gray, "⎿ ") + status + "\n"
	if hint := hintFor(r.err); hint != "" {
		out += "  " + r.ink.paint(style.Gray, hint) + "\n"
	}

	return out
}

// announce returns the console hook of a run: it says, through w, when a
// console opens and in which mode, and when it closes. The banner is the only
// sign that the device owns the terminal, printed at the only moment that is
// known. A cooked terminal echoed the Ctrl-C that interrupted the run, with
// no line end: the close starts a line of its own then; a raw one ended its
// line already.
func (r *runCmd) announce(w *crlfWriter, interrupted func() bool) func(open, raw bool) {
	return func(open, raw bool) {
		if !open && interrupted() {
			r.lineEnded.Store(true)

			if !raw {
				fmt.Fprint(w, "\n")
			}
		}

		w.endLine()

		switch {
		case open && raw:
			r.raw.Store(true)
			fmt.Fprint(w, rawBanner(r.ink, r.width)+"\n")
		case open:
			text := ": Enter sends a line · Ctrl-D ends your input · Ctrl-C ends the run"
			fmt.Fprint(w, consoleLine(r.ink, false, ansi.Truncate(text, max(0, r.width-1-ansi.StringWidth(lineTitle)), "…"))+"\n")
		default:
			fmt.Fprint(w, consoleLine(r.ink, raw, " closed")+"\n")
		}
	}
}

// The titles of the console lines.
const (
	rawTitle  = "▌ raw console"
	lineTitle = "▌ line console"
)

// consoleLine renders a line about a console, its title followed by text, in
// the console's colour: violet for raw, where the device owns the terminal,
// blue for line, where the terminal edits the line. The title stays bold
// without colour, the one sign of the mode then.
func consoleLine(paint ink, raw bool, text string) string {
	title, code := lineTitle, style.Blue
	if raw {
		title, code = rawTitle, style.Magenta
	}

	return strong(paint.paint(code, title)) + paint.paint(code, text)
}

// rawBanner says that every key now goes to the device and how to get back,
// fitted to width: the extras go first, then what the device owns, and the
// way out is never dropped.
func rawBanner(paint ink, width int) string {
	parts := []string{"every key goes to the device", "Ctrl-A x ends it", "Ctrl-A twice sends Ctrl-A", "Ctrl-A e local echo"}

	fits := func(parts []string) bool {
		return ansi.StringWidth(rawTitle+": "+strings.Join(parts, " · ")) < width
	}

	for len(parts) > 2 && !fits(parts) {
		parts = parts[:len(parts)-1]
	}

	if !fits(parts) {
		parts = parts[1:]
	}

	return consoleLine(paint, true, ": "+strings.Join(parts, " · "))
}

// formatElapsed renders a run's duration: tenths below ten seconds, then
// seconds, then minutes and seconds.
func formatElapsed(elapsed time.Duration) string {
	const tenthsUpTo = 10 * time.Second

	switch {
	case elapsed < tenthsUpTo:
		return fmt.Sprintf("%.1fs", elapsed.Seconds())
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds", int(elapsed.Seconds()))
	default:
		return elapsed.Truncate(time.Second).String()
	}
}

// outcome is how a run ended, as far as the landing is concerned.
type outcome int

const (
	outcomeDone outcome = iota
	outcomeInterrupted
	outcomeBadArgs         // InvalidArgument: the arguments are wrong
	outcomeRefused         // FailedPrecondition: busy, reserved, the same command running, shutting down
	outcomeAborted         // the module failed or panicked
	outcomeNoCommand       // NotFound: the command is gone
	outcomeNoDevice        // NotFound: the device is gone
	outcomeUnreachable     // Unavailable: the agent is gone
	outcomeUnauthenticated // the identity is anonymous
	outcomeOther           // a client-side or stream failure
)

// classify maps a run's error to its outcome by its connect code; NotFound is
// told apart by the agent's message, the only sign on the wire.
func classify(err error) outcome {
	switch {
	case err == nil:
		return outcomeDone
	case errors.Is(err, errInterrupted):
		return outcomeInterrupted
	}

	switch connect.CodeOf(err) {
	case connect.CodeInvalidArgument:
		return outcomeBadArgs
	case connect.CodeFailedPrecondition:
		return outcomeRefused
	case connect.CodeAborted:
		return outcomeAborted
	case connect.CodeUnavailable:
		return outcomeUnreachable
	case connect.CodeUnauthenticated:
		return outcomeUnauthenticated
	case connect.CodeNotFound:
		return classifyNotFound(err)
	default:
		return outcomeOther
	}
}

// classifyNotFound tells a gone device from a gone command.
func classifyNotFound(err error) outcome {
	switch {
	case strings.Contains(err.Error(), "no such device"):
		return outcomeNoDevice
	case strings.Contains(err.Error(), "no such command"):
		return outcomeNoCommand
	default:
		return outcomeOther
	}
}

// recorded reports whether a run that ended so goes into the history, as the
// last line of a recent device: one the agent accepted. A typo must not.
func (o outcome) recorded() bool {
	switch o {
	case outcomeBadArgs, outcomeNoCommand, outcomeNoDevice, outcomeUnreachable, outcomeUnauthenticated:
		return false
	default:
		return true
	}
}

// hintFor returns what to do about a refusal, or "".
func hintFor(err error) string {
	if err == nil {
		return ""
	}

	switch connect.CodeOf(err) {
	case connect.CodeFailedPrecondition:
		text := err.Error()

		switch {
		case strings.Contains(text, "is in use by"):
			return "busy until that command returns; a forced unlock does not end it"
		case strings.Contains(text, "is locked by"):
			return "a reservation: wait, or `unlock force` releases it (a forced unlock never ends a running command)"
		case strings.Contains(text, "is already running"):
			return "the same command never runs twice at once; wait for it or run a different one"
		case strings.Contains(text, "shutting down"):
			return "the agent is stopping; try again once it is back"
		}
	case connect.CodePermissionDenied:
		return "`unlock force` releases another user's reservation"
	case connect.CodeUnauthenticated:
		return "start dutctl with -u name@host"
	}

	return ""
}

// logLineCap bounds the diagnostics waiting to be printed above the prompt.
const logLineCap = 256

// switchWriter carries the client's diagnostics while the TUI runs: above
// the prompt while the TUI draws, straight to the terminal while a run
// holds it, and after the TUI. A write never waits for the TUI, whose
// event loop may be the writer or may hold the terminal for a run.
type switchWriter struct {
	out   io.Writer   // the terminal's error stream, with line ends while raw
	lines chan string // on their way above the prompt, in order

	mu       sync.Mutex
	released bool
	closed   bool
}

func newSwitchWriter(out io.Writer) *switchWriter {
	return &switchWriter{out: out, lines: make(chan string, logLineCap)}
}

func (w *switchWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.released || w.closed {
		return w.out.Write(data)
	}

	select {
	case w.lines <- strings.TrimRight(string(data), "\n"):
	default: // shortcut: a burst beyond logLineCap is dropped while the TUI draws; queue to a file if it matters
	}

	return len(data), nil
}

// forward hands the queued lines to the TUI, which prints them above the
// prompt, until close. It runs on a goroutine of its own.
func (w *switchWriter) forward(send func(tea.Msg)) {
	for line := range w.lines {
		send(logMsg(line))
	}
}

// release sends the diagnostics straight to the terminal while on: a run
// holds it.
func (w *switchWriter) release(on bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.released = on
}

// close sends the diagnostics straight to the terminal from now on: the
// TUI is over. It ends forward.
func (w *switchWriter) close() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.closed {
		w.closed = true
		close(w.lines)
	}
}
