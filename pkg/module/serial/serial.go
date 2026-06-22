// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package serial provides a dutagent module that listens on a defined COM port.
package serial

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
	"github.com/tarm/serial"
)

func init() {
	module.Register(module.Record{
		ID:  "serial",
		New: func() module.Module { return &Serial{} },
	})
}

// DefaultBaudRate is the default baud rate for the serial connection.
const DefaultBaudRate = 115200

// pairsDrain is the time the module continues reading serial output after
// sending the last response in expect-send mode, so the output of the
// triggered command is visible before the connection closes.
const pairsDrain = 1 * time.Second

// reconnectInterval is the delay between attempts to reopen the serial device
// after it has disappeared (e.g. an FTDI chip that powers down with the DUT).
const reconnectInterval = 500 * time.Millisecond

// deviceLossGraceDefault is how long the module tolerates receiving no serial
// data before it suspects the device is gone (rather than merely idle) and
// verifies by checking the device node. With ReadTimeout set the driver uses
// VMIN=0, so an idle read returns io.EOF exactly like a removed device would —
// the grace plus the node check tell a quiet but healthy console apart from a
// device that has actually vanished. Kept short so a real disconnect is noticed
// quickly, but long enough not to probe on every idle read.
const deviceLossGraceDefault = 2 * time.Second

// maxMatchWindow bounds the buffer of recent serial output kept for regex
// matching. Matching is always done against the tail of the output, so old
// bytes can be discarded; the cap keeps memory and regex cost bounded while
// staying far larger than any realistic prompt or expect pattern.
const maxMatchWindow = 64 * 1024

// stepKind distinguishes the two kinds of step in a serial sequence.
type stepKind int

const (
	stepExpect stepKind = iota // wait until pattern matches the serial output
	stepSend                   // write data to the serial port
)

// seqStep is one step of a serial automation sequence. The module walks the
// steps in order: it waits for each expect-step's pattern to appear in the
// output, and writes each send-step's data to the port. A single expect
// pattern and the legacy expect-send pairs both compile down to a slice of
// these, so the run loop has exactly one code path for all non-interactive
// modes.
type seqStep struct {
	kind    stepKind
	pattern *regexp.Regexp // set when kind == stepExpect
	data    []byte         // set when kind == stepSend (empty = send nothing)
}

// serialPort is the subset of the serial device used by Run. It is satisfied by
// *serial.Port and allows a fake to be injected in tests via Serial.dialPort.
type serialPort interface {
	io.ReadWriteCloser
	Flush() error
}

// Serial is a module that provides an interactive serial connection to a DUT.
type Serial struct {
	Port string // Port is the path to the serial device on the dutagent.
	Baud int    // Baud is the baud rate of the serial device. If unset, DefaultBaudRate is used.

	steps   []seqStep     // steps is the ordered expect/send sequence (empty = interactive mode).
	timeout time.Duration // timeout is the maximum time to wait for the sequence to complete.

	// dialPort opens the serial port. It defaults to openPort and is overridden
	// in tests to inject a fake. Set lazily in Run so the zero value works.
	dialPort func() (serialPort, error)

	// portPresent reports whether the configured device node still exists. It
	// defaults to a filesystem stat of Port and is overridden in tests. Used to
	// tell a benign idle-timeout EOF apart from real device loss.
	portPresent func() bool

	// deviceLossGrace overrides deviceLossGraceDefault in tests so the device-loss
	// path can be exercised quickly. Zero means use the default.
	deviceLossGrace time.Duration
}

// Ensure implementing the Module interface.
var _ module.Module = &Serial{}

const abstract = `Serial connection to the DUT
`

const usage = `
ARGUMENTS:
	[-t <duration>] [<expect> [<response> <expect> <response> ...]]
	[-t <duration>] expect:<regex>|send:<data> [expect:<regex>|send:<data> ...]

`

const description = `
The serial module provides an interactive connection to the DUT's serial port.
Input from the client is forwarded to the serial port, and output from the serial port is displayed.

Modes of operation:
  - Interactive (no arguments): read and write until terminated by a signal (e.g. Ctrl-C).
  - Expect (1 argument): wait for the regex to match on the serial output, then exit.
  - Expect-send (even number of arguments >= 2): pass pattern/response pairs.
    For each pair, the module waits for the pattern to match and then sends the
    response to the serial port. Pairs are processed in order; after the last
    pair matches the module reads serial output for 1 more second so the output
    of the triggered command is visible, then exits.
  - Sequence (every argument carries an "expect:" or "send:" tag): an ordered
    list of steps run one after another. An expect-step waits for its regex to
    match the serial output; a send-step writes its data to the port. Unlike
    expect-send pairs, the steps may appear in any order, so a sequence can
    begin with a send (e.g. an Enter to wake the console) or chain several
    sends or expects in a row. The whole sequence shares the -t deadline. If
    the last step is a send, the module drains output for 1 more second before
    exiting; if it is an expect, it exits as soon as that pattern matches.

      e.g.: send:"\n" expect:"login:" send:"root\n" expect:"# " send:"reboot\n"

If the serial device disappears mid-session (e.g. an FTDI chip that powers down
when the DUT loses power), the module waits for it to reappear and reconnects
automatically instead of ending the session.

The expect string supports regular expressions according to [1].
The optional -t flag specifies the maximum time to wait.
Quote strings containing spaces or special characters. E.g.: "(?i)hello\s+world"
Response and send strings support C-style escape sequences: \n, \r, \t, \\, \xHH.

[1] https://golang.org/s/re2syntax.
`

func (s *Serial) Help() string {
	log.Println("serial module: Help called")

	help := strings.Builder{}
	help.WriteString(abstract)
	help.WriteString(usage)
	fmt.Fprintf(&help, "Configured COM port is  %q with baud rate %d.\n", s.Port, s.Baud)
	help.WriteString(description)

	return help.String()
}

func (s *Serial) Init() error {
	log.Println("serial module: Init called")

	if s.Port == "" {
		return fmt.Errorf("COM port is not set")
	}

	if s.Baud == 0 {
		s.Baud = DefaultBaudRate
	}

	// Note: We don't open the port here to allow dutagent to start
	// even if the serial device is not yet available (e.g., powered off).
	// The port will be opened when Run() is called.

	return nil
}

func (s *Serial) Deinit() error {
	log.Println("serial module: Deinit called")

	return nil
}

// Run bridges the DUT's serial port to the client console.
//
// Concurrency model (deliberately minimal to be race- and deadlock-free):
//
//   - The main loop is the sole owner of all match state (expect/pairs/draining,
//     the match window, and the CSI remainder). Nothing else touches it, so no
//     lock is needed to protect it.
//   - One "stdin pump" goroutine forwards client keystrokes to the port. It is
//     the only goroutine that may briefly outlive Run; it never reads or writes
//     match state. It unblocks via stdin EOF, which the agent guarantees by
//     closing the stdin channel during session teardown.
//   - One "stdout pump" goroutine performs every client write, so the main loop
//     can keep matching and draining without blocking on a slow client, and so
//     a vanished client can never wedge the main loop (writes abort on context
//     cancellation).
//   - Port writes (stdin forwarding + auto-responses) are serialised by portMu,
//     which is never held across a channel operation, so it cannot deadlock.
//
//nolint:gocognit,cyclop,funlen,gocyclo,maintidx
func (s *Serial) Run(ctx context.Context, session module.Session, args ...string) error {
	log.Println("serial module: Run called")

	err := s.evalArgs(args)
	if err != nil {
		return err
	}

	dial := s.dialPort
	if dial == nil {
		dial = s.openPort
	}

	present := s.portPresent
	if present == nil {
		present = func() bool {
			_, statErr := os.Stat(s.Port)

			return statErr == nil
		}
	}

	grace := s.deviceLossGrace
	if grace == 0 {
		grace = deviceLossGraceDefault
	}

	port, err := dial()
	if err != nil {
		return err
	}

	// done is closed when Run returns, signalling the stdin pump to stop writing
	// to the (about to be closed) port.
	done := make(chan struct{})
	defer close(done)

	// portMu guards the current port handle, which is replaced when the serial
	// device disappears (e.g. an FTDI chip that powers down with the DUT) and is
	// reopened once it reappears — see reconnect below. The main loop owns the
	// handle and is its only writer, so it may read port directly; the stdin
	// pump (another goroutine) goes through writeToPort under the lock.
	var portMu sync.Mutex

	storePort := func(next serialPort) {
		portMu.Lock()
		defer portMu.Unlock()

		port = next
	}

	closePort := func() {
		portMu.Lock()
		defer portMu.Unlock()

		if port != nil {
			_ = port.Close()
			port = nil
		}
	}

	defer closePort()

	// Discard any stale bytes left in the kernel/driver RX buffer from a
	// previous session, otherwise the user sees data from the last boot.
	flushErr := port.Flush()
	if flushErr != nil {
		log.Printf("serial module: flush failed: %v", flushErr)
	}

	stdin, stdout, _ := session.Console() // stderr intentionally unused: serial output goes to stdout only

	log.Printf("serial module: connected to %s at %d baud", s.Port, s.Baud)
	fmt.Fprintf(stdout, "--- Connected to %s at %d baud ---\n", s.Port, s.Baud)

	// baseCtx tracks external cancellation (client disconnect / agent teardown).
	// It is never replaced, so the stdout pump keeps flushing even while a
	// derived deadline (expect timeout or post-match drain) is counting down.
	baseCtx := ctx

	writeToPort := func(data []byte) {
		portMu.Lock()
		defer portMu.Unlock()

		if port == nil {
			return // device is gone; drop input until it reconnects
		}

		_, werr := port.Write(data)
		if werr != nil {
			select {
			case <-done: // port closed on Run exit — not an error.
			default:
				log.Printf("serial module: error writing to serial port: %v", werr)
			}
		}
	}

	s.startStdinPump(stdin, done, writeToPort)

	emit, flushAndWait := newStdoutPump(baseCtx, stdout)
	defer flushAndWait(false) // ensure the pump goroutine is released on every exit path

	// loopCtx carries the active deadline: the expect timeout first, then the
	// post-match drain. It is always derived from baseCtx, so external
	// cancellation still propagates through it.
	loopCtx := baseCtx

	var loopCancel context.CancelFunc

	if s.timeout > 0 {
		log.Printf("serial module: setting timeout of %s", s.timeout)
		loopCtx, loopCancel = context.WithTimeout(baseCtx, s.timeout)
	}

	defer func() {
		if loopCancel != nil {
			loopCancel()
		}
	}()

	var (
		remainder   []byte // partial CSI sequence carried across reads
		matchWindow []byte // bounded window of recent output for regex matching
		currentStep int    // cursor into s.steps
		draining    bool
	)

	// lastData is the time of the last data-bearing read; with no data for the
	// grace period it triggers the device-loss check.
	lastData := time.Now()

	//nolint:fatcontext // intentional: the post-match drain replaces the loop deadline
	startDrain := func() {
		if loopCancel != nil {
			loopCancel()
		}

		loopCtx, loopCancel = context.WithTimeout(baseCtx, pairsDrain)
		draining = true

		log.Printf("serial module: draining serial output for %s before closing", pairsDrain)
	}

	// advanceSends fires consecutive send-steps at the cursor, writing each to
	// the port, until the cursor reaches an expect-step or the end of the
	// sequence. It is called once before the first read (so a sequence may begin
	// with a send) and again after every expect-step matches.
	advanceSends := func() {
		for currentStep < len(s.steps) && s.steps[currentStep].kind == stepSend {
			data := s.steps[currentStep].data
			currentStep++

			if len(data) > 0 {
				writeToPort(data)
			}
		}
	}

	// reconnect closes the vanished port and retries opening it until the device
	// reappears, the deadline (watchCtx) fires, or the session is cancelled. Once
	// it returns, either a fresh port is in place or watchCtx is done and the
	// loop's select handles it. Mirrors tio's auto-reconnect so a DUT power-cycle
	// that drops the FTDI device does not end the session.
	reconnect := func(watchCtx context.Context) {
		closePort()

		emit([]byte("\n--- Serial device disconnected, waiting to reconnect ---\n"))
		log.Printf("serial module: device %s disconnected, waiting to reconnect", s.Port)

		for {
			fresh, dialErr := dial()
			if dialErr == nil {
				flushErr := fresh.Flush()
				if flushErr != nil {
					log.Printf("serial module: flush after reconnect failed: %v", flushErr)
				}

				storePort(fresh)

				emit([]byte("\n--- Serial device reconnected ---\n"))
				log.Printf("serial module: device %s reconnected", s.Port)

				return
			}

			select {
			case <-watchCtx.Done():
				return
			case <-time.After(reconnectInterval):
			}
		}
	}

	const bufferSize = 4096

	readBuffer := make([]byte, bufferSize)

	// Fire any leading send-steps before the first read, so a sequence may begin
	// by sending (e.g. an Enter to wake the console) rather than expecting. If
	// the sequence is sends only, drain briefly so the DUT's response to the
	// final input is visible, then exit via the drain deadline below.
	advanceSends()

	if len(s.steps) > 0 && currentStep >= len(s.steps) {
		startDrain()
	}

	for {
		select {
		case <-loopCtx.Done():
			if baseCtx.Err() != nil {
				// External cancellation: client disconnect or agent teardown.
				// The client may no longer be reading, so do not wait on a flush.
				log.Println("serial module: context cancelled, closing")

				return baseCtx.Err()
			}

			// One of our own deadlines fired.
			if draining {
				emit([]byte("\n--- Pattern matched, connection closed ---"))
				flushAndWait(true)

				return nil
			}

			emit([]byte("\n--- Timeout reached, no match found ---"))
			flushAndWait(true)

			if len(s.steps) == 1 && s.steps[0].kind == stepExpect {
				return fmt.Errorf("timeout of %s reached, pattern %q not found", s.timeout, s.steps[0].pattern)
			}

			return fmt.Errorf("timeout of %s reached, expect-send sequence not completed", s.timeout)
		default:
		}

		nRead, readErr := port.Read(readBuffer)

		// A hard read error (EIO/ENXIO/…) means the device errored outright — wait
		// for it to come back instead of ending the session. A plain read timeout
		// or io.EOF is NOT a hard error: with VMIN=0 an idle read returns io.EOF,
		// so it is handled below together with zero-length reads.
		if readErr != nil && readErr != io.EOF && !strings.Contains(readErr.Error(), "timeout") {
			reconnect(loopCtx)

			lastData = time.Now()

			continue
		}

		if nRead == 0 {
			// No data this cycle. Usually a benign idle read — but a removed or
			// powered-down device idles identically (io.EOF / zero bytes), so once
			// no data has arrived for the grace period, verify the device node
			// still exists and reconnect if it has vanished. reconnect returns
			// false only when the deadline or an external cancellation fired; the
			// next loop iteration's select then handles it.
			if time.Since(lastData) > grace && !present() {
				reconnect(loopCtx)

				lastData = time.Now()
			}

			continue
		}

		lastData = time.Now()

		// Filter cursor/query CSI sequences (SGR colour is preserved),
		// reconstructing sequences split across reads via remainder.
		chunk := readBuffer[:nRead]
		if len(remainder) > 0 {
			chunk = append(remainder, chunk...)
			remainder = nil
		}

		out := filterOutputCSI(chunk, &remainder)
		if len(out) == 0 {
			continue
		}

		// Display everything immediately, including partial lines (e.g. prompts
		// without a trailing newline). out is a fresh slice owned by the pump.
		emit(out)

		// Matching is skipped while draining and in interactive mode (no steps).
		if draining || len(s.steps) == 0 {
			continue
		}

		matchWindow = append(matchWindow, out...)
		if len(matchWindow) > maxMatchWindow {
			matchWindow = matchWindow[len(matchWindow)-maxMatchWindow:]
		}

		// Walk the sequence: satisfy every expect-step the current window already
		// matches, firing the send-steps that follow each match. The cursor rests
		// on an expect-step here, because advanceSends consumed any leading or
		// trailing sends after the previous iteration.
		for currentStep < len(s.steps) && s.steps[currentStep].kind == stepExpect {
			loc := s.steps[currentStep].pattern.FindIndex(matchWindow)
			if loc == nil {
				break
			}

			matchWindow = matchWindow[loc[1]:] // consume through the match
			currentStep++

			advanceSends() // fire the send-steps that follow this match

			if currentStep < len(s.steps) {
				continue // more steps remain; keep matching the current window
			}

			// Sequence complete. If it ended on a send, drain so the DUT's
			// response to the final input is visible; if it ended on an expect,
			// the match itself is the completion, so exit immediately.
			if s.steps[len(s.steps)-1].kind == stepSend {
				startDrain()
			} else {
				emit([]byte("\n--- Pattern matched, connection closed ---"))
				flushAndWait(true)

				return nil
			}

			break
		}
	}
}

const stdinBufSize = 256

// startStdinPump forwards client keystrokes to the serial port until stdin
// reaches EOF. It is the only goroutine that may outlive Run; it touches no
// match state, so it cannot race with the main loop.
func (s *Serial) startStdinPump(stdin io.Reader, done <-chan struct{}, writeToPort func([]byte)) {
	go func() {
		buf := make([]byte, stdinBufSize)

		for {
			nRead, err := stdin.Read(buf)
			if nRead > 0 {
				select {
				case <-done:
					return // Run exited — do not write to the closing port.
				default:
					writeToPort(buf[:nRead])
				}
			}

			if err != nil {
				return // EOF on session teardown, or a read error.
			}
		}
	}()
}

const stdoutQueueLen = 256

// newStdoutPump starts a single goroutine that performs every client write and
// returns two closures:
//
//   - emit(data) queues data for the client. It never blocks the caller
//     indefinitely: if baseCtx is cancelled (client gone) the data is dropped
//     rather than wedging the main loop. The caller must not reuse data
//     afterwards; pass a fresh slice.
//   - flushAndWait(deliver) shuts the pump down. With deliver=true it waits for
//     all queued data to be written (safe only while the client is still
//     reading, i.e. on graceful completion). With deliver=false it just releases
//     the pump without waiting; it is idempotent and safe to defer on every path.
func newStdoutPump(baseCtx context.Context, stdout io.Writer) (func([]byte), func(bool)) {
	outCh := make(chan []byte, stdoutQueueLen)
	pumpDone := make(chan struct{})

	go func() {
		defer close(pumpDone)

		for data := range outCh {
			_, _ = stdout.Write(data) // ChanWriter.Write only fails on misuse
		}
	}()

	var closeOnce sync.Once

	closeOut := func() { closeOnce.Do(func() { close(outCh) }) }

	emit := func(data []byte) {
		select {
		case outCh <- data:
		case <-baseCtx.Done(): // client gone — drop rather than block forever.
		}
	}

	flushAndWait := func(deliver bool) {
		closeOut()

		if deliver {
			<-pumpDone
		}
	}

	return emit, flushAndWait
}

// pairStride is the number of positional arguments per expect-send pair.
const pairStride = 2

// Tag prefixes that mark a tagged-sequence argument.
const (
	expectTag = "expect:"
	sendTag   = "send:"
)

func (s *Serial) evalArgs(args []string) error {
	fs := flag.NewFlagSet("serial", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // Suppress default error output
	fs.DurationVar(&s.timeout, "t", 0, "timeout duration (e.g. 3m, 30s)")

	err := fs.Parse(args)
	if err != nil {
		return fmt.Errorf("failed to parse arguments: %w", err)
	}

	positional := fs.Args()

	// Tagged-sequence mode is selected when the first argument carries a tag.
	// It allows arbitrarily ordered expect/send steps (e.g. a leading send).
	if len(positional) > 0 && isTaggedStep(positional[0]) {
		return s.evalSequenceArgs(positional)
	}

	return s.evalLegacyArgs(positional)
}

// evalLegacyArgs parses the backward-compatible positional argument forms into
// s.steps: no args (interactive), one arg (single expect), or an even number of
// args (expect-send pairs).
func (s *Serial) evalLegacyArgs(positional []string) error {
	switch len(positional) {
	case 0:
		// Interactive mode: no steps.
	case 1:
		// Single-expect mode.
		log.Printf("serial module: Will wait for pattern: %q", positional[0])

		pattern, compileErr := regexp.Compile(positional[0])
		if compileErr != nil {
			return fmt.Errorf("invalid regular expression: %w", compileErr)
		}

		s.steps = []seqStep{{kind: stepExpect, pattern: pattern}}
	default:
		// Expect-send pairs mode.
		if len(positional)%pairStride != 0 {
			return fmt.Errorf("expect-send requires an even number of arguments, got %d", len(positional))
		}

		s.steps = make([]seqStep, 0, len(positional))

		for idx := 0; idx < len(positional); idx += pairStride {
			pattern, compileErr := regexp.Compile(positional[idx])
			if compileErr != nil {
				return fmt.Errorf("invalid regular expression %q: %w", positional[idx], compileErr)
			}

			log.Printf("serial module: Pair %d: pattern=%q response=%q", idx/pairStride+1, positional[idx], positional[idx+1])

			s.steps = append(s.steps,
				seqStep{kind: stepExpect, pattern: pattern},
				seqStep{kind: stepSend, data: unescape(positional[idx+1])},
			)
		}
	}

	return nil
}

// isTaggedStep reports whether arg carries an "expect:" or "send:" tag.
func isTaggedStep(arg string) bool {
	return strings.HasPrefix(arg, expectTag) || strings.HasPrefix(arg, sendTag)
}

// evalSequenceArgs parses tagged-sequence arguments into s.steps. Every
// argument must carry a tag; mixing tagged and untagged arguments is rejected
// so a malformed command fails loudly instead of being silently misread.
func (s *Serial) evalSequenceArgs(args []string) error {
	s.steps = make([]seqStep, 0, len(args))

	for idx, arg := range args {
		switch {
		case strings.HasPrefix(arg, expectTag):
			expr := arg[len(expectTag):]

			pattern, compileErr := regexp.Compile(expr)
			if compileErr != nil {
				return fmt.Errorf("step %d: invalid regular expression %q: %w", idx+1, expr, compileErr)
			}

			log.Printf("serial module: Step %d: expect=%q", idx+1, expr)

			s.steps = append(s.steps, seqStep{kind: stepExpect, pattern: pattern})
		case strings.HasPrefix(arg, sendTag):
			data := arg[len(sendTag):]

			log.Printf("serial module: Step %d: send=%q", idx+1, data)

			s.steps = append(s.steps, seqStep{kind: stepSend, data: unescape(data)})
		default:
			return fmt.Errorf("step %d %q: in sequence mode every argument must start with %q or %q",
				idx+1, arg, expectTag, sendTag)
		}
	}

	return nil
}

const readTimeout = 100 * time.Millisecond

//nolint:ireturn // intentional: returns the serialPort interface so a fake can be injected in tests
func (s *Serial) openPort() (serialPort, error) {
	config := &serial.Config{
		Name:        s.Port,
		Baud:        s.Baud,
		ReadTimeout: readTimeout, // Short timeout for responsive context checking
	}

	port, err := serial.OpenPort(config)
	if err != nil {
		return nil, fmt.Errorf("failed to open serial port %s: %w", s.Port, err)
	}

	return port, nil
}

// escByte is the ASCII escape character that starts ANSI/VT escape sequences.
const escByte = 0x1b

// csiPrefixLen is the length of the CSI prefix "ESC[".
const csiPrefixLen = 2

// Bytes that terminate an ANSI string sequence.
const (
	stByte  = 0x9c // single-byte String Terminator (C1)
	belByte = 0x07 // BEL — also terminates an OSC sequence
)

// isStringSeqIntroducer reports whether b, following ESC, begins an ANSI string
// sequence: DCS (P), OSC (]), SOS (X), PM (^) or APC (_). These carry terminal
// queries and responses (e.g. capability reports like the WezTerm "name=" DCS),
// never display content, so they are dropped in full.
func isStringSeqIntroducer(b byte) bool {
	return b == 'P' || b == ']' || b == 'X' || b == '^' || b == '_'
}

// stringSeqEnd returns the index of the last byte of the ANSI string sequence
// that starts at data[escIdx] (with introducer data[escIdx+1]), or -1 if the
// sequence is not terminated within data — in which case the caller carries it
// to the next read. The sequence ends at ST ("ESC \" or the single-byte 0x9c);
// an OSC may also end at BEL.
func stringSeqEnd(data []byte, escIdx int) int {
	osc := data[escIdx+1] == ']'

	for pos := escIdx + csiPrefixLen; pos < len(data); pos++ {
		switch {
		case data[pos] == stByte:
			return pos
		case osc && data[pos] == belByte:
			return pos
		case data[pos] == escByte:
			if pos+1 >= len(data) {
				return -1 // ESC at end — the ST may be split across reads
			}

			if data[pos+1] == '\\' {
				return pos + 1 // "ESC \" = ST
			}
			// ESC followed by anything else is not a terminator; keep scanning.
		}
	}

	return -1
}

// filterOutputCSI removes terminal control sequences from serial output data,
// except for SGR (Select Graphic Rendition, final byte 'm') which handles colors
// and styles. It drops CSI sequences (cursor positioning, screen clearing,
// queries) and ANSI string sequences (DCS/OSC/SOS/PM/APC terminal query
// responses), while preserving coloured output.
//
// Incomplete sequences at the end of data are stored in remainder so they can be
// prepended to the next buffer read and reconstituted correctly.
//
//nolint:cyclop,varnamelen
func filterOutputCSI(data []byte, remainder *[]byte) []byte {
	result := make([]byte, 0, len(data))
	*remainder = nil

	for i := 0; i < len(data); i++ {
		if data[i] != escByte {
			result = append(result, data[i])

			continue
		}

		// ESC at end of buffer: might be the start of a sequence split across reads.
		if i+1 >= len(data) {
			*remainder = []byte{escByte}

			break
		}

		if isStringSeqIntroducer(data[i+1]) {
			end := stringSeqEnd(data, i)
			if end < 0 {
				// Incomplete string sequence — carry it over to the next read.
				*remainder = make([]byte, len(data)-i)
				copy(*remainder, data[i:])

				break
			}

			// Drop the whole sequence; the outer loop's i++ advances past data[end].
			i = end

			continue
		}

		if data[i+1] != '[' {
			// ESC not followed by '[' or a string introducer — emit as-is.
			result = append(result, data[i])

			continue
		}

		// CSI sequence: ESC [
		// Find the extent of this CSI sequence.
		j := i + csiPrefixLen

		for j < len(data) && data[j] >= 0x30 && data[j] <= 0x3f {
			j++ // parameter bytes
		}

		for j < len(data) && data[j] >= 0x20 && data[j] <= 0x2f {
			j++ // intermediate bytes
		}

		if j >= len(data) {
			// Incomplete sequence at end of buffer — carry it over to the next read.
			*remainder = make([]byte, len(data)-i)
			copy(*remainder, data[i:])

			break
		}

		if data[j] == 'm' {
			// SGR (colors/styles) — keep it.
			result = append(result, data[i:j+1]...)
		}

		// All other CSI sequences are dropped, including malformed ones where
		// data[j] is not a valid final byte (0x40–0x7E). The byte at data[j]
		// is consumed by setting i = j; the outer loop's i++ then advances past
		// it. Silently dropping malformed sequences is safer than emitting
		// partial escape bytes which could corrupt the terminal display.
		i = j
	}

	return result
}

// unescape converts C-style escape sequences in s to their byte equivalents.
// Supported sequences: \n (newline), \r (carriage return), \t (tab),
// \\ (backslash), \xHH (hex byte). Unrecognised sequences are emitted as-is.
//
//nolint:cyclop // a flat switch over the supported escapes; splitting it would not help readability
func unescape(s string) []byte {
	out := make([]byte, 0, len(s))

	for idx := 0; idx < len(s); idx++ {
		if s[idx] != '\\' || idx+1 >= len(s) {
			out = append(out, s[idx])

			continue
		}

		idx++

		switch s[idx] {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case '\\':
			out = append(out, '\\')
		case 'x':
			if idx+hexDigits < len(s) {
				hi, hiOK := fromHex(s[idx+1])
				lo, loOK := fromHex(s[idx+hexDigits])

				if hiOK && loOK {
					out = append(out, hi<<nibbleBits|lo)
					idx += hexDigits

					continue
				}
			}

			out = append(out, '\\', 'x')
		default:
			out = append(out, '\\', s[idx])
		}
	}

	return out
}

// Constants for \xHH hex-escape decoding.
const (
	hexDigits       = 2  // number of hex digits in a \xHH escape
	nibbleBits      = 4  // bit width of one hex nibble
	hexLetterOffset = 10 // value of hex digit 'a'/'A'
)

// fromHex converts a single ASCII hex digit to its nibble value.
// Returns (value, true) on success or (0, false) if digit is not a hex character.
func fromHex(digit byte) (byte, bool) {
	switch {
	case digit >= '0' && digit <= '9':
		return digit - '0', true
	case digit >= 'a' && digit <= 'f':
		return digit - 'a' + hexLetterOffset, true
	case digit >= 'A' && digit <= 'F':
		return digit - 'A' + hexLetterOffset, true
	default:
		return 0, false
	}
}
