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

// sendDrain is the time the module keeps reading serial output after the last
// send-step of a sequence, so the output triggered by that final input is
// visible before the connection closes.
const sendDrain = time.Second

// defaultDelay is the pause applied before each send when no Delay is
// configured. A small pause makes sends robust against consoles that present a
// prompt slightly before the tty is ready to read; configure Delay as "0s" to
// disable it.
const defaultDelay = 50 * time.Millisecond

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

// Serial is a module that automates interaction with a DUT's serial console.
type Serial struct {
	Port  string // Port is the path to the serial device on the dutagent.
	Baud  int    // Baud is the baud rate of the serial device. If unset, DefaultBaudRate is used.
	Delay string // Delay is the pause before each send (e.g. "200ms") to pace input. Default: 50ms; "0s" disables.

	steps   []seqStep     // steps is the ordered expect/send sequence (empty = monitor mode).
	timeout time.Duration // timeout is the maximum time to wait for the sequence to complete.
	delay   time.Duration // delay is the parsed Delay, applied before each send-step.

	// dialPort opens the serial port. It defaults to openPort and is overridden
	// in tests to inject a fake. Set lazily in Run so the zero value works.
	dialPort func() (serialPort, error)
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
The serial module automates interaction with the DUT's serial console: it reads
the serial output, optionally matches it against expect patterns, and writes
responses back to the port.

Modes of operation:
  - Monitor (no arguments): stream the serial output until the session is cancelled.
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

	delayDesc := s.Delay
	if delayDesc == "" {
		delayDesc = defaultDelay.String()
	}

	fmt.Fprintf(&help, "A delay of %s is applied before each send.\n", delayDesc)

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

func (s *Serial) Deinit() error {
	log.Println("serial module: Deinit called")

	return nil
}

// Run bridges the DUT's serial port to the client. It reads serial output,
// streams it to the client, and drives the expect/send sequence by matching
// patterns against the output and writing send-step responses to the port.
//
// Concurrency model (deliberately minimal to be race- and deadlock-free):
//
//   - The main loop is the sole owner of the port and all match state (steps,
//     draining, the match window, and the CSI remainder). Nothing else touches
//     them, so no lock is needed.
//   - One "stdout pump" goroutine performs every client write, so the main loop
//     can keep matching and draining without blocking on a slow client, and so
//     a vanished client can never wedge the main loop (writes abort on context
//     cancellation).
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

	port, err := dial()
	if err != nil {
		return err
	}

	defer func() { _ = port.Close() }()

	// Discard any stale bytes left in the kernel/driver RX buffer from a
	// previous session, otherwise the user sees data from the last boot.
	flushErr := port.Flush()
	if flushErr != nil {
		log.Printf("serial module: flush failed: %v", flushErr)
	}

	// stdin is unused: this module never forwards client input; it only reads the
	// port, matches, and writes send-step responses. stderr is unused too.
	_, stdout, _ := session.Console()

	log.Printf("serial module: connected to %s at %d baud", s.Port, s.Baud)
	fmt.Fprintf(stdout, "--- Connected to %s at %d baud ---\n", s.Port, s.Baud)

	emit, flushAndWait := newStdoutPump(ctx, stdout)
	defer flushAndWait(false) // ensure the pump goroutine is released on every exit path

	// loopCtx carries the active deadline: the expect timeout first, then the
	// post-send drain. It is always derived from ctx, so external cancellation
	// still propagates through it.
	loopCtx := ctx

	var loopCancel context.CancelFunc

	if s.timeout > 0 {
		log.Printf("serial module: setting timeout of %s", s.timeout)
		loopCtx, loopCancel = context.WithTimeout(ctx, s.timeout)
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

	//nolint:fatcontext // intentional: the post-send drain replaces the loop deadline
	startDrain := func() {
		if loopCancel != nil {
			loopCancel()
		}

		loopCtx, loopCancel = context.WithTimeout(ctx, sendDrain)
		draining = true

		log.Printf("serial module: draining serial output for %s before closing", sendDrain)
	}

	// advanceSends fires consecutive send-steps at the cursor, writing each to
	// the port, until the cursor reaches an expect-step or the end of the
	// sequence. It is called once before the first read (so a sequence may begin
	// with a send) and again after every expect-step matches.
	//
	// When a delay is configured it pauses before every send to pace input — both
	// between back-to-back sends and after a prompt match (some consoles drop
	// characters sent the instant the prompt appears). The pause is interruptible
	// so an expired deadline or a cancelled session still ends the run promptly.
	advanceSends := func() {
		for currentStep < len(s.steps) && s.steps[currentStep].kind == stepSend {
			if s.delay > 0 {
				select {
				case <-time.After(s.delay):
				case <-loopCtx.Done():
					return
				}
			}

			data := s.steps[currentStep].data
			currentStep++

			if len(data) > 0 {
				_, werr := port.Write(data)
				if werr != nil {
					log.Printf("serial module: error writing to serial port: %v", werr)
				}
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
			if ctx.Err() != nil {
				// External cancellation: client disconnect or agent teardown.
				// The client may no longer be reading, so do not wait on a flush.
				log.Println("serial module: context cancelled, closing")

				return ctx.Err()
			}

			// One of our own deadlines fired.
			if draining {
				emit([]byte("\n--- Sequence complete, connection closed ---"))
				flushAndWait(true)

				return nil
			}

			emit([]byte("\n--- Timeout reached, no match found ---"))
			flushAndWait(true)

			if len(s.steps) == 1 && s.steps[0].kind == stepExpect {
				return fmt.Errorf("timeout of %s reached, pattern %q not found", s.timeout, s.steps[0].pattern)
			}

			return fmt.Errorf("timeout of %s reached, sequence not completed", s.timeout)
		default:
		}

		nRead, readErr := port.Read(readBuffer)
		if readErr != nil {
			// With VMIN=0 an idle read returns io.EOF, and some platforms report a
			// "timeout" error — both just mean "no data yet", so keep looping and
			// stay responsive to ctx. Any other error means the device failed.
			if readErr == io.EOF || strings.Contains(readErr.Error(), "timeout") {
				continue
			}

			return fmt.Errorf("serial read error: %w", readErr)
		}

		if nRead == 0 {
			continue
		}

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

		// Matching is skipped while draining and in monitor mode (no steps).
		if draining || len(s.steps) == 0 {
			continue
		}

		matchWindow = append(matchWindow, out...)
		if len(matchWindow) > maxMatchWindow {
			// Copy the tail to the front of the same backing array rather than
			// resliding the start forward, which would leak the discarded head
			// until the slice grew enough to force a reallocation. This keeps the
			// backing array bounded at ~maxMatchWindow, as the cap promises.
			matchWindow = append(matchWindow[:0], matchWindow[len(matchWindow)-maxMatchWindow:]...)
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
