// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package serial provides a dutagent module that streams a DUT's serial console,
// runs a scripted send/expect sequence, or bridges the console interactively.
package serial

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
	"go.bug.st/serial"
)

func init() {
	module.Register(module.Record{
		ID:  "serial",
		New: func() module.Module { return &Serial{} },
	})
}

// DefaultBaudRate is the default baud rate for the serial connection.
const DefaultBaudRate = 115200

// readTimeout is how long a single port read blocks before returning so the
// loop can re-check the context. It bounds how promptly the global timeout and
// cancellation are honored.
const readTimeout = 100 * time.Millisecond

// defaultDelay is the pause applied before each send when no Delay is
// configured. It makes sends robust against consoles that present a prompt
// slightly before the tty is ready to read; configure delay "0s" to disable.
const defaultDelay = 50 * time.Millisecond

// sendDrain is how long the module keeps reading after a sequence whose last
// step is a send, so the DUT's reply to that final input is visible before the
// connection closes.
const sendDrain = time.Second

// portOpener opens a serial device. It is the injection point that lets tests
// substitute a fake port for the real hardware.
type portOpener func(name string, baud int) (port, error)

// Serial streams the DUT's serial output, runs a scripted send/expect sequence,
// or bridges the console interactively, depending on its arguments.
type Serial struct {
	Port  string // Port is the path to the serial device on the dutagent.
	Baud  int    // Baud is the baud rate of the serial device. If unset, DefaultBaudRate is used.
	Delay string // Delay is the pause before each send (e.g. "200ms") to pace input. Default 50ms; "0s" disables.

	delay time.Duration // delay is the parsed Delay, applied before each send.

	// drainTimeout overrides the post-send drain window; 0 uses sendDrain. It
	// exists so tests can shorten the drain; production leaves it 0.
	drainTimeout time.Duration

	// open opens the serial port. It defaults to defaultOpenPort in Run; tests
	// set it to a fake. Opening happens per Run, never on the struct.
	open portOpener

	// portPresent reports whether the configured device node still exists; it
	// defaults to a filesystem stat of Port and is overridden in tests. It lets
	// the read loop tell a quiet-but-healthy console apart from a vanished
	// device when deciding whether to reconnect.
	portPresent func() bool

	// deviceLossGrace overrides deviceLossGraceDefault in tests so the
	// device-loss path can be exercised quickly. Zero uses the default.
	deviceLossGrace time.Duration
}

// Ensure implementing the Module interface.
var _ module.Module = &Serial{}

const abstract = `Serial connection to the DUT
`

const usage = `
ARGUMENTS:
	[-t <duration>] [-eol cr|lf|crlf|none] [-keep-escapes]                 (monitor: stream output)
	[-t <duration>] [-keep-escapes] -i                                     (interactive console)
	[-t <duration>] [-eol cr|lf|crlf|none] [-keep-escapes] [--] <step>...  (run a step sequence)

	step := expect <regex> | send <data> | send-raw <data>

`

const description = `
The serial module interacts with the DUT's serial console. It runs in one of
three modes, selected by its arguments:

  - MONITOR (no arguments): stream the serial output to the client until the
    session is cancelled, or until -t elapses (a success).
  - INTERACTIVE (-i): bridge the client console to the serial port, forwarding
    keystrokes to the port and streaming output back, until the session is
    cancelled or -t elapses. Takes no steps.
  - STEP SEQUENCE (one or more steps): run the scripted steps in order (see
    below); suits scripts and automated callers.

Serial output is forwarded to the client in monitor and interactive modes, while
waiting for an expect, and (if the last step is a send) for a moment afterwards
so its reply is visible.

If the serial device disappears mid-session (e.g. an FTDI chip that powers down
when the DUT loses power), the module waits for it to reappear and reconnects
automatically instead of ending the session.

STEPS (executed in order; the run fails on the first expect that times out):
	expect <regex>   Wait until the serial output matches the RE2 regular
	                 expression [1]. A plain string is a valid regex that
	                 matches itself; escape regex meta characters
	                 (. $ * + ? ( ) [ ] { } ^ | \) to match them literally,
	                 e.g. expect '192\.168\.0\.1'.
	send <data>      Write <data> followed by the configured line ending
	                 (see -eol) to the port.
	send-raw <data>  Write <data> verbatim, with no line ending appended.

send / send-raw data supports the escapes \r \n \t \\ and \xNN (e.g. \x03 for
Ctrl-C). Each step value is one argument, so quote values containing spaces.

If the last step is a send, the module keeps showing output for a moment
afterwards so the DUT's reply to that final input is visible.

FLAGS (before the steps):
	-t <duration>          Global timeout for the whole run (e.g. 30s, 3m).
	                       0 (default) means no timeout.
	-eol cr|lf|crlf|none   Line ending appended by 'send'. Default: cr ('\r'),
	                       which is what serial consoles expect on Enter.
	-keep-escapes          Keep terminal escape sequences (cursor moves, colour,
	                       queries) instead of stripping them from the output.
	-i                     Interactive: bridge the client console to the serial
	                       port. Cannot be combined with steps.

Terminal escape sequences (cursor moves, colour, queries) are stripped from the
output before it is shown or matched, unless -keep-escapes is given. Expect
matching uses a rolling window of the most recent 64 KiB of output, so a single
pattern cannot span more than that. Match on distinctive markers/prompts
rather than '^'/'$' anchors. Note the DUT may echo what you send, so an expect
right after a send can match your own input rather than the device's reply.

EXAMPLES:
	monitor the console:            (no arguments)
	interactive console:            -i
	wait for a boot marker:         -- expect 'Welcome to'
	login then run a command:       -- expect 'login:' send root expect '# ' send reboot
	send Ctrl-C then expect shell:  -- send-raw '\x03' expect '$ '

[1] https://golang.org/s/re2syntax
`

func (s *Serial) Help() string {
	help := strings.Builder{}
	help.WriteString(abstract)
	help.WriteString(usage)
	fmt.Fprintf(&help, "Configured COM port is %q with baud rate %d.\n", s.Port, s.Baud)
	help.WriteString(description)

	return help.String()
}

// Init validates the configuration and parses the send delay. It deliberately
// does not open the serial port, so dutagent can start even when the device is
// unavailable (e.g. powered off); the port is opened per Run instead.
func (s *Serial) Init(ctx context.Context) error {
	if s.Port == "" {
		return fmt.Errorf("COM port is not set")
	}

	if s.Baud == 0 {
		s.Baud = DefaultBaudRate
		log.FromContext(ctx).Debug(fmt.Sprintf("no baud rate configured, using default %d", DefaultBaudRate))
	}

	s.delay = defaultDelay

	if s.Delay != "" {
		parsed, err := time.ParseDuration(s.Delay)
		if err != nil {
			return fmt.Errorf("invalid delay %q: %w", s.Delay, err)
		}

		s.delay = parsed
	}

	// Note: We don't open the port here to allow dutagent to start
	// even if the serial device is not yet available (e.g., powered off).
	// The port will be opened when Run() is called.

	return nil
}

// Deinit does nothing: the port is opened and closed within each Run, never
// held on the struct between runs, so there is nothing to release.
func (s *Serial) Deinit(_ context.Context) error {
	// Nothing to clean up: the port is opened and closed within each Run
	// (see Run's defer), never held on the struct between runs.
	return nil
}

// defaultOpenPort is the default portOpener; it opens a real serial device.
func defaultOpenPort(name string, baud int) (port, error) {
	serialPort, err := serial.Open(name, &serial.Mode{BaudRate: baud})
	if err != nil {
		return nil, fmt.Errorf("failed to open serial port %s: %w", name, err)
	}

	// Short read timeout so the read loop stays responsive to context
	// cancellation; a timed-out read returns (0, nil), not an error.
	err = serialPort.SetReadTimeout(readTimeout)
	if err != nil {
		_ = serialPort.Close()

		return nil, fmt.Errorf("failed to set read timeout on %s: %w", name, err)
	}

	return serialPort, nil
}

// reopenFunc returns a closure that reopens the configured device; the engine
// uses it to reconnect after the device disappears.
func (s *Serial) reopenFunc(opener portOpener) func() (port, error) {
	return func() (port, error) {
		return opener(s.Port, s.Baud)
	}
}

// presentFunc reports whether the configured device node still exists. It
// defaults to a filesystem stat of Port and is overridden in tests.
func (s *Serial) presentFunc() func() bool {
	if s.portPresent != nil {
		return s.portPresent
	}

	return func() bool {
		_, err := os.Stat(s.Port)

		return err == nil
	}
}

// graceDur is the quiet period tolerated before the device-loss node check.
func (s *Serial) graceDur() time.Duration {
	if s.deviceLossGrace > 0 {
		return s.deviceLossGrace
	}

	return deviceLossGraceDefault
}

// Run opens the configured serial port and either streams its output or
// executes a step sequence. With no steps it runs in monitor mode, streaming
// until the session is cancelled or -t elapses (both a success). With steps it
// sends and expects in order, failing on the first expect that times out.
//
//nolint:cyclop,funlen // monitor/sequence dispatch with pacing and drain; the branch count is inherent
func (s *Serial) Run(ctx context.Context, session module.Session, args ...string) error {
	// The logger carried on ctx is already scoped to this module by the agent
	// (scope "module", with module/device/command attributes), so the module
	// only adds what is specific to this run.
	l := log.FromContext(ctx)

	// Parse into LOCAL state every Run — the module instance is shared and
	// reused across RPCs, so nothing per-run may live on the struct.
	cfg, err := parseArgs(args)
	if err != nil {
		return err
	}

	opener := s.open
	if opener == nil {
		opener = defaultOpenPort
	}

	serialPort, err := opener(s.Port, s.Baud)
	if err != nil {
		return err
	}

	// Discard any stale bytes left in the kernel/driver RX buffer from a
	// previous session, otherwise a step could match data from the last boot.
	err = serialPort.ResetInputBuffer()
	if err != nil {
		l.Warn("reset input buffer failed", "err", err)
	}

	l.Info(fmt.Sprintf("connected to %s at %d baud", s.Port, s.Baud))

	// loopCtx carries the per-run deadline (-t). The original ctx is kept for the
	// post-send drain so the drain gets its own full window.
	loopCtx := ctx

	if cfg.timeout > 0 {
		var cancel context.CancelFunc

		l.Debug(fmt.Sprintf("setting global timeout %s", cfg.timeout))
		loopCtx, cancel = context.WithTimeout(ctx, cfg.timeout)

		defer cancel()
	}

	if cfg.interactive {
		return s.runInteractive(ctx, loopCtx, session, serialPort, opener, cfg)
	}

	clientOut := newClientWriter(session)
	clientOut.markerf("--- Connected to %s at %d baud ---\n", s.Port, s.Baud)

	eng := newEngine(serialPort, clientOut, !cfg.keepEscapes)
	eng.withReconnect(s.reopenFunc(opener), s.presentFunc(), s.graceDur())

	defer eng.closeCurrent()

	// Monitor mode: no steps — stream the console until cancelled or -t elapses.
	if len(cfg.steps) == 0 {
		l.Debug("monitor mode; streaming until cancelled")

		err = eng.monitor(loopCtx)
		if err != nil {
			return fmt.Errorf("monitor: %w", err)
		}

		clientOut.markerf("--- Connection closed ---\n")

		return nil
	}

	total := len(cfg.steps)

	for idx, curStep := range cfg.steps {
		switch curStep.kind {
		case stepExpect:
			err = eng.readUntil(loopCtx, curStep.expect)
			if err != nil {
				return stepError(idx, curStep, cfg.timeout, err)
			}

			clientOut.markerf("--- [%d/%d] matched %q ---\n", idx+1, total, curStep.label())
		case stepSend, stepSendRaw:
			// Pace input: pause before each send (interruptible by the deadline).
			err = sleepCtx(loopCtx, s.delay)
			if err != nil {
				return fmt.Errorf("step %d (send %q): %w", idx+1, curStep.label(), err)
			}

			err = eng.write(curStep.payload)
			if err != nil {
				return fmt.Errorf("step %d (send %q): %w", idx+1, curStep.label(), err)
			}

			clientOut.markerf("--- [%d/%d] sent %q ---\n", idx+1, total, curStep.label())
		}
	}

	// If the sequence ended on a send, drain briefly so the DUT's reply to the
	// final input is visible. Uses the original ctx (its own window).
	if cfg.steps[total-1].kind != stepExpect {
		drainFor := sendDrain
		if s.drainTimeout > 0 {
			drainFor = s.drainTimeout
		}

		err = eng.drain(ctx, drainFor)
		if err != nil {
			return fmt.Errorf("draining after final send: %w", err)
		}
	}

	clientOut.markerf("--- Script completed ---\n")

	return nil
}

// runInteractive bridges the client console to the serial port for a live
// session: keystrokes are forwarded to the port and port output is streamed
// back, until the client disconnects (ctx cancel) or -t elapses. The device is
// reopened automatically if it disappears mid-session.
func (s *Serial) runInteractive(
	baseCtx, loopCtx context.Context,
	session module.Session,
	initialPort port,
	opener portOpener,
	cfg scriptConfig,
) error {
	l := log.FromContext(baseCtx)

	stdin, stdout, _ := session.Console()

	l.Debug("interactive mode; bridging console to serial port")
	fmt.Fprintf(stdout, "--- Connected to %s at %d baud ---\n", s.Port, s.Baud)

	eng := newEngine(initialPort, stdout, !cfg.keepEscapes)
	eng.withReconnect(s.reopenFunc(opener), s.presentFunc(), s.graceDur())

	defer eng.closeCurrent()

	// Context cancellation — the client disconnecting (an interactive quit or
	// agent teardown) or the -t deadline — is the normal end of an open-ended
	// session, as in monitor mode, so interactive returns nil for it. Only a
	// genuine engine error (e.g. a broken client stream) fails the run.
	err := eng.interactive(loopCtx, stdin)
	if err != nil {
		return fmt.Errorf("interactive: %w", err)
	}

	fmt.Fprintf(stdout, "\n--- Connection closed ---\n")

	return nil
}

// sleepCtx pauses for d, returning ctx.Err() if ctx is done first. A
// non-positive d returns nil immediately.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stepError annotates an expect failure with the step number and pattern, and
// gives a clear message for the common timeout/cancellation cases.
func stepError(idx int, failedStep step, timeout time.Duration, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded) && timeout > 0:
		return fmt.Errorf("step %d (expect %q): timeout of %s reached without match", idx+1, failedStep.label(), timeout)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("step %d (expect %q): deadline reached without match", idx+1, failedStep.label())
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("step %d (expect %q): canceled", idx+1, failedStep.label())
	default:
		return fmt.Errorf("step %d (expect %q): %w", idx+1, failedStep.label(), err)
	}
}

// clientWriter forwards serial output to the client (it is the engine's output
// sink in monitor and step modes) and tracks whether the stream is at the start
// of a line. Status lines go through markerf, which inserts a newline first when
// the preceding output (e.g. a prompt with no trailing newline) did not end one
// — so every marker lands on its own line.
//
// Interactive mode instead uses the stdout writer from session.Console().
type clientWriter struct {
	session     module.Session
	atLineStart bool
}

func newClientWriter(session module.Session) *clientWriter {
	return &clientWriter{session: session, atLineStart: true}
}

func (w *clientWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		w.session.Print(string(p))
		w.atLineStart = p[len(p)-1] == '\n'
	}

	return len(p), nil
}

// markerf writes a status line, prefixing a newline when the previous output
// did not end one, then tracking whether this line left the stream at a line
// start.
func (w *clientWriter) markerf(format string, args ...any) {
	if !w.atLineStart {
		w.session.Print("\n")
	}

	msg := fmt.Sprintf(format, args...)
	w.session.Print(msg)
	w.atLineStart = len(msg) > 0 && msg[len(msg)-1] == '\n'
}
