// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package serial provides a dutagent module that listens on a defined COM port.
package serial

import (
	"bytes"
	"context"
	"errors"
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

// expectSendPair holds a compiled regex pattern and the bytes to send when it matches.
// A nil or empty response means the module exits on match without sending anything.
type expectSendPair struct {
	pattern  *regexp.Regexp
	response []byte
}

// Serial is a module that provides an interactive serial connection to a DUT.
type Serial struct {
	Port string // Port is the path to the serial device on the dutagent.
	Baud int    // Baud is the baud rate of the serial device. If unset, DefaultBaudRate is used.

	expect       *regexp.Regexp   // expect is a pattern to match against the serial output (single-expect mode).
	pairs        []expectSendPair // pairs are the expect-send pairs (expect-send mode).
	timeout      time.Duration    // timeout is the maximum time to wait for the expect pattern to match.
	csiRemainder []byte           // csiRemainder holds an incomplete CSI sequence carried over across buffer reads.
}

// Ensure implementing the Module interface.
var _ module.Module = &Serial{}

const abstract = `Serial connection to the DUT
`
const usage = `
ARGUMENTS:
	[-t <duration>] [<expect> [<response> <expect> <response> ...]]

`
const description = `
The serial module provides an interactive connection to the DUT's serial port.
Input from the client is forwarded to the serial port, and output from the serial port is displayed.

Modes of operation:
  - Interactive (no arguments): read and write until terminated by a signal (e.g. Ctrl-C).
  - Expect (1 argument): wait for the regex to match on the serial output, then exit.
  - Expect-send (even number of arguments >= 2): pass pattern/response pairs.
    For each pair, the module waits for the pattern to match and then sends the
    response to the serial port. Pairs are processed in order; the module exits
    after the last pair matches.

The expect string supports regular expressions according to [1].
The optional -t flag specifies the maximum time to wait.
Quote strings containing spaces or special characters. E.g.: "(?i)hello\s+world"
Response strings support C-style escape sequences: \n, \r, \t, \\, \xHH.

[1] https://golang.org/s/re2syntax.
`

func (s *Serial) Help() string {
	log.Println("serial module: Help called")

	help := strings.Builder{}
	help.WriteString(abstract)
	help.WriteString(usage)
	help.WriteString(fmt.Sprintf("Configured COM port is  %q with baud rate %d.\n", s.Port, s.Baud))
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

//nolint:cyclop,funlen,gocognit,gocyclo,maintidx
func (s *Serial) Run(ctx context.Context, session module.Session, args ...string) error {
	log.Println("serial module: Run called")

	err := s.evalArgs(args)
	if err != nil {
		return err
	}

	port, err := s.openPort()
	if err != nil {
		return err
	}
	defer port.Close()

	stdin, stdout, _ := session.Console() // stderr intentionally unused: serial output goes to stdout only

	log.Printf("serial module: connected to %s at %d baud", s.Port, s.Baud)
	fmt.Fprintf(stdout, "--- Connected to %s at %d baud ---\n", s.Port, s.Baud)

	var cancel context.CancelFunc

	if s.timeout > 0 {
		log.Printf("serial module: setting timeout of %s", s.timeout)
		ctx, cancel = context.WithTimeout(ctx, s.timeout)

		defer cancel()
	}

	// done is closed when Run() exits to signal goroutines to stop.
	done := make(chan struct{})
	defer close(done)

	// matchCh signals the main select when a pattern match is detected inside the
	// flush timer goroutine, which cannot return from Run() directly.
	matchCh := make(chan struct{}, 1)

	// portMu serializes all writes to the serial port across goroutines (stdin
	// forwarding, auto-responses). This prevents interleaved writes when user
	// input and an auto-response arrive concurrently.
	var portMu sync.Mutex

	writeToPort := func(data []byte) error {
		portMu.Lock()
		defer portMu.Unlock()

		_, writeErr := port.Write(data)
		if writeErr != nil {
			log.Printf("serial module: error writing to serial port: %v", writeErr)
		}

		return writeErr
	}

	// currentPair is the index of the next expect-send pair to match.
	// Accessed under mutex (below) because it is also read in timer callbacks.
	currentPair := 0

	// checkLineMatch reports whether line satisfies the current expect condition.
	// Must be called while holding mutex.
	// Returns (response to send to the port, exit) where exit signals Run() to return.
	checkLineMatch := func(line string) (response []byte, exit bool) {
		if s.expect != nil {
			if s.expect.MatchString(line) {
				return nil, true
			}

			return nil, false
		}

		if currentPair >= len(s.pairs) {
			return nil, false
		}

		if !s.pairs[currentPair].pattern.MatchString(line) {
			return nil, false
		}

		response = s.pairs[currentPair].response
		currentPair++

		return response, currentPair >= len(s.pairs)
	}

	// Forward client stdin to serial port.
	// DSR suppression (preventing CPR injection) is handled on the output side by
	// filterOutputCSI, which strips non-SGR CSI sequences — including DSR (ESC[6n) —
	// from serial output before it reaches the client terminal. The terminal never sees
	// a DSR query and therefore never injects CPR responses into stdin.
	const stdinBufSize = 256

	type readResult struct {
		data []byte
		err  error
	}

	readResultCh := make(chan readResult, 1)

	go func() { // inner goroutine — exits when fromClientWorker closes stdinCh on session teardown.
		buf := make([]byte, stdinBufSize)

		for {
			n, err := stdin.Read(buf)
			cp := make([]byte, n)
			copy(cp, buf[:n])
			readResultCh <- readResult{data: cp, err: err}

			if err != nil {
				return
			}
		}
	}()

	go func() { // outer goroutine — exits promptly when done is closed.
		for {
			select {
			case <-done:
				return
			case res := <-readResultCh:
				if res.err != nil {
					select {
					case <-done:
						return // Run() exited — suppress spurious error log.
					default:
					}

					if ctx.Err() != nil {
						return
					}

					log.Printf("serial module: error reading from stdin: %v", res.err)

					return
				}

				select {
				case <-done:
					return
				default:
				}

				if writeToPort(res.data) != nil {
					select {
					case <-done:
						return // port closed on Run() exit — not an error.
					default:
					}

					return
				}
			}
		}
	}()

	const bufferSize = 4096

	readBuffer := make([]byte, bufferSize)
	lineBuffer := &bytes.Buffer{}

	// mutex protects lineBuffer and currentPair which are accessed from the main
	// loop and the flush timer goroutine.
	var mutex sync.Mutex

	// flushGen is a generation counter that invalidates stale timer callbacks.
	// Every access (increment in the main loop, read in callbacks, increment in
	// the shutdown defer) is performed while holding mutex, so no separate
	// synchronisation is needed.
	var flushGen uint64

	defer func() {
		mutex.Lock()

		flushGen++ // Invalidate any pending timer callbacks.

		mutex.Unlock()
	}()

	const flushTimeout = 100 * time.Millisecond

	for {
		select {
		case <-matchCh:
			fmt.Fprintln(stdout, "\n--- Pattern matched, connection closed ---")

			return nil
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				fmt.Fprintln(stdout, "\n--- Timeout reached, no match found ---")

				if s.expect != nil {
					return fmt.Errorf("timeout of %s reached, pattern %q not found", s.timeout, s.expect)
				}

				return fmt.Errorf("timeout of %s reached, expect-send sequence not completed", s.timeout)
			}

			fmt.Fprintln(stdout, "\n--- Connection closed ---")

			return ctx.Err()
		default:
			sbytes, err := port.Read(readBuffer)
			if err != nil {
				// Ignore timeout errors as these are expected with the read timeout config
				if err != io.EOF && !strings.Contains(err.Error(), "timeout") {
					return fmt.Errorf("error reading from serial port: %w", err)
				}

				continue
			}

			if sbytes == 0 {
				continue
			}

			// Filter CSI sequences from serial output that could cause
			// cursor positioning artifacts. Color/style sequences (SGR)
			// are preserved. Prepend any partial CSI sequence left over
			// from the previous read to handle sequences split across buffers.
			chunk := readBuffer[:sbytes]
			if len(s.csiRemainder) > 0 {
				chunk = append(s.csiRemainder, chunk...)
				s.csiRemainder = nil
			}

			outData := filterOutputCSI(chunk, &s.csiRemainder)
			if len(outData) == 0 {
				continue
			}

			mutex.Lock()

			// Process the data read character by character
			for _, b := range outData {
				lineBuffer.WriteByte(b)

				if b != '\n' && lineBuffer.Len() < 1024 {
					continue
				}

				line := lineBuffer.String()
				lineBuffer.Reset()

				_, writeErr := stdout.Write([]byte(line))
				if writeErr != nil {
					mutex.Unlock()

					return fmt.Errorf("error writing to stdout: %w", writeErr)
				}

				response, exit := checkLineMatch(line)

				if exit {
					mutex.Unlock()

					if len(response) > 0 {
						_ = writeToPort(response)
					}

					fmt.Fprintln(stdout, "\n--- Pattern matched, connection closed ---")

					return nil
				}

				if len(response) > 0 {
					// Intermediate pair matched: send response and continue waiting.
					mutex.Unlock()

					_ = writeToPort(response)

					mutex.Lock()
				}
			}

			// If there's data remaining in the line buffer (no newline yet),
			// schedule a flush so prompts like "login: " appear promptly.
			// Each timer captures its generation; stale callbacks are no-ops.
			if lineBuffer.Len() > 0 {
				flushGen++
				thisGen := flushGen

				time.AfterFunc(flushTimeout, func() {
					// Phase 1: drain the buffer under the lock (fast, no I/O).
					mutex.Lock()

					if flushGen != thisGen || lineBuffer.Len() == 0 {
						mutex.Unlock()

						return
					}

					line := lineBuffer.String()
					lineBuffer.Reset()

					response, exit := checkLineMatch(line)

					mutex.Unlock()

					// Phase 2: write to stdout outside the lock.
					// ChanWriter.Write blocks on an unbuffered channel send; holding
					// the mutex during that send could deadlock the main loop.
					// Skip the write if Run() has already returned to avoid a stuck
					// goroutine when toClientWorker is no longer receiving.
					select {
					case <-done:
						return
					default:
					}

					_, _ = stdout.Write([]byte(line))

					if len(response) > 0 {
						_ = writeToPort(response)
					}

					if exit {
						log.Printf("serial module: pattern matched in flush")

						select {
						case matchCh <- struct{}{}:
						default: // already signalled
						}
					}
				})
			}

			mutex.Unlock()
		}
	}
}

//nolint:cyclop
func (s *Serial) evalArgs(args []string) error {
	fs := flag.NewFlagSet("serial", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // Suppress default error output
	fs.DurationVar(&s.timeout, "t", 0, "timeout duration (e.g. 3m, 30s)")

	err := fs.Parse(args)
	if err != nil {
		return fmt.Errorf("failed to parse arguments: %w", err)
	}

	positional := fs.Args()

	switch len(positional) {
	case 0:
		// Interactive mode: no expect pattern, no pairs.
	case 1:
		// Single-expect mode (backward compatible).
		log.Printf("serial module: Will wait for pattern: %q", positional[0])

		re, compileErr := regexp.Compile(positional[0])
		if compileErr != nil {
			return fmt.Errorf("invalid regular expression: %w", compileErr)
		}

		s.expect = re
	default:
		// Expect-send pairs mode.
		if len(positional)%2 != 0 {
			return fmt.Errorf("expect-send requires an even number of arguments, got %d", len(positional))
		}

		s.pairs = make([]expectSendPair, 0, len(positional)/2)

		for i := 0; i < len(positional); i += 2 {
			re, compileErr := regexp.Compile(positional[i])
			if compileErr != nil {
				return fmt.Errorf("invalid regular expression %q: %w", positional[i], compileErr)
			}

			log.Printf("serial module: Pair %d: pattern=%q response=%q", i/2+1, positional[i], positional[i+1])

			s.pairs = append(s.pairs, expectSendPair{
				pattern:  re,
				response: unescape(positional[i+1]),
			})
		}
	}

	return nil
}

const readTimeout = 100 * time.Millisecond

func (s *Serial) openPort() (*serial.Port, error) {
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

// filterOutputCSI removes CSI sequences from serial output data, except for
// SGR (Select Graphic Rendition, final byte 'm') which handles colors and styles.
// This prevents cursor positioning, screen clearing, and terminal query sequences
// from affecting the client terminal, while preserving colored output.
//
// Incomplete CSI sequences at the end of data are stored in remainder so they
// can be prepended to the next buffer read and reconstituted correctly.
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

		// ESC at end of buffer: might be the start of a CSI sequence split across reads.
		if i+1 >= len(data) {
			*remainder = []byte{escByte}

			break
		}

		if data[i+1] != '[' {
			// ESC not followed by '[' — not a CSI sequence, emit as-is.
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
func unescape(s string) []byte {
	out := make([]byte, 0, len(s))

	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			out = append(out, s[i])

			continue
		}

		i++

		switch s[i] {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case '\\':
			out = append(out, '\\')
		case 'x':
			if i+2 < len(s) {
				hi, hiOK := fromHex(s[i+1])
				lo, loOK := fromHex(s[i+2])

				if hiOK && loOK {
					out = append(out, hi<<4|lo)
					i += 2

					continue
				}
			}

			out = append(out, '\\', 'x')
		default:
			out = append(out, '\\', s[i])
		}
	}

	return out
}

// fromHex converts a single ASCII hex digit to its nibble value.
// Returns (value, true) on success or (0, false) if digit is not a hex character.
func fromHex(digit byte) (byte, bool) {
	switch {
	case digit >= '0' && digit <= '9':
		return digit - '0', true
	case digit >= 'a' && digit <= 'f':
		return digit - 'a' + 10, true
	case digit >= 'A' && digit <= 'F':
		return digit - 'A' + 10, true
	default:
		return 0, false
	}
}
