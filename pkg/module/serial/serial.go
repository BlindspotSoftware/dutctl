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

// Serial is a module that provides an interactive serial connection to a DUT.
type Serial struct {
	Port string // Port is the path to the serial device on the dutagent.
	Baud int    // Baud is the baud rate of the serial device. Is unset, DefaultBaudRate is used.

	expect  *regexp.Regexp // expect is a pattern to match against the serial output.
	timeout time.Duration  // timeout is the maximum time to wait for the expect pattern to match.
}

// Ensure implementing the Module interface.
var _ module.Module = &Serial{}

const abstract = `Serial connection to the DUT
`
const usage = `
ARGUMENTS:
	[-t <duration>] [<expect>]

`
const description = `
The serial module provides an interactive connection to the DUT's serial port.
Input from the client is forwarded to the serial port, and output from the serial port is displayed.
If a regex is provided, the module will wait for the regex to match on the serial output,
then exit with success. If no expect string is provided, the module will read from the serial port
until it is terminated by a signal (e.g. Ctrl-C).
The expect string supports regular expressions according to [1].
The optional -t flag specifies the maximum time to wait for the regex to match.
Quote the expect string if it contains spaces or special characters. E.g.: "(?i)hello\s+world!? dutctl"

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

	stdin, stdout, _ := session.Console()

	log.Printf("serial module: connected to %s at %d baud", s.Port, s.Baud)
	fmt.Fprintf(stdout, "--- Connected to %s at %d baud ---\n", s.Port, s.Baud)

	var cancel context.CancelFunc

	if s.timeout > 0 {
		log.Printf("serial module: setting timeout of %s", s.timeout)
		ctx, cancel = context.WithTimeout(ctx, s.timeout)

		defer cancel()
	}

	// done is closed when Run() exits to signal the stdin goroutine to stop.
	done := make(chan struct{})
	defer close(done)

	// matchCh receives a value when a pattern is matched inside the flush
	// timer goroutine, which cannot return from Run() directly.
	matchCh := make(chan struct{}, 1)

	// Forward client stdin to serial port.
	// Strip ANSI CSI sequences from stdin, since the remote system may send terminal
	// queries (e.g. DSR) that cause the local terminal to inject responses (e.g. CPR)
	// into stdin, which would corrupt the serial input.
	const stdinBufSize = 256

	go func() {
		buf := make([]byte, stdinBufSize)

		for {
			nRead, err := stdin.Read(buf)
			if err != nil {
				select {
				case <-done:
					return // Run() exited — suppress spurious error log.
				default:
				}

				if ctx.Err() != nil {
					return
				}

				log.Printf("serial module: error reading from stdin: %v", err)

				return
			}

			data := stripCSI(buf[:nRead])

			if len(data) == 0 {
				continue
			}

			select {
			case <-done:
				return
			default:
			}

			_, writeErr := port.Write(data)
			if writeErr != nil {
				select {
				case <-done:
					return // port closed on Run() exit — not an error.
				default:
					log.Printf("serial module: error writing to serial port: %v", writeErr)
				}

				return
			}
		}
	}()

	const bufferSize = 4096

	readBuffer := make([]byte, bufferSize)
	lineBuffer := &bytes.Buffer{}

	// mutex protects lineBuffer which is accessed from the main loop
	// and the flush timer goroutine.
	var mutex sync.Mutex

	flushBuffer := func() {
		if lineBuffer.Len() == 0 {
			return
		}

		line := lineBuffer.String()
		_, _ = stdout.Write([]byte(line)) // ChanWriter.Write always returns nil

		if s.expect != nil && s.expect.MatchString(line) {
			log.Printf("serial module: pattern matched in flush")

			select {
			case matchCh <- struct{}{}:
			default: // already signalled
			}
		}

		lineBuffer.Reset()
	}

	var flushTimer *time.Timer

	defer func() {
		if flushTimer != nil {
			flushTimer.Stop()
		}
	}()

	const flushTimeout = 100 * time.Millisecond

	for {
		select {
		case <-matchCh:
			fmt.Fprintln(stdout, "\n--- Pattern matched, connection closed ---")

			return nil
		case <-ctx.Done():
			mutex.Lock()

			// Flush any remaining data before closing.
			flushBuffer()

			mutex.Unlock()

			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				fmt.Fprintln(stdout, "\n--- Timeout reached, no match found ---")

				return fmt.Errorf("timeout of %s reached, pattern %q not found", s.timeout, s.expect)
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
			// are preserved.
			outData := filterOutputCSI(readBuffer[:sbytes])
			if len(outData) == 0 {
				continue
			}

			mutex.Lock()

			// Stop pending flush timer since we have new data.
			if flushTimer != nil {
				flushTimer.Stop()
			}

			// Process the data read character by character
			for _, b := range outData {
				lineBuffer.WriteByte(b)

				// If we reach a newline or a buffer limit, process the line
				if b == '\n' || lineBuffer.Len() >= 1024 {
					line := lineBuffer.String()

					_, writeErr := stdout.Write([]byte(line))
					if writeErr != nil {
						mutex.Unlock()

						return fmt.Errorf("error writing to stdout: %w", writeErr)
					}

					// Check for regex match if we have one
					if s.expect != nil && s.expect.MatchString(line) {
						mutex.Unlock()

						fmt.Fprintln(stdout, "\n--- Pattern matched, connection closed ---")

						return nil // Success - pattern found
					}

					lineBuffer.Reset()
				}
			}

			// If there's data remaining in the line buffer (no newline yet),
			// schedule a flush so prompts like "login: " appear promptly.
			if lineBuffer.Len() > 0 {
				flushTimer = time.AfterFunc(flushTimeout, func() {
					mutex.Lock()
					flushBuffer()
					mutex.Unlock()
				})
			}

			mutex.Unlock()
		}
	}
}

func (s *Serial) evalArgs(args []string) error {
	fs := flag.NewFlagSet("serial", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // Suppress default error output
	fs.DurationVar(&s.timeout, "t", 0, "timeout duration (e.g. 3m, 30s)")

	err := fs.Parse(args)
	if err != nil {
		return fmt.Errorf("failed to parse arguments: %w", err)
	}

	// Get the expect string if provided (args after flags)
	var expectPattern string
	if fs.NArg() > 0 {
		expectPattern = fs.Arg(0)
		log.Printf("serial module: Will wait for pattern: %q", expectPattern)
	}

	if expectPattern != "" {
		var err error

		s.expect, err = regexp.Compile(expectPattern)
		if err != nil {
			return fmt.Errorf("invalid regular expression: %w", err)
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

// csiPrefixLen is the length of the CSI prefix "ESC[".
const csiPrefixLen = 2

// filterOutputCSI removes CSI sequences from serial output data, except for
// SGR (Select Graphic Rendition, final byte 'm') which handles colors and styles.
// This prevents cursor positioning, screen clearing, and terminal query sequences
// from affecting the client terminal, while preserving colored output.
//
//nolint:cyclop,varnamelen
func filterOutputCSI(data []byte) []byte {
	result := make([]byte, 0, len(data))

	for i := 0; i < len(data); i++ {
		if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '[' {
			// Find the extent of this CSI sequence.
			j := i + csiPrefixLen

			for j < len(data) && data[j] >= 0x30 && data[j] <= 0x3f {
				j++ // parameter bytes
			}

			for j < len(data) && data[j] >= 0x20 && data[j] <= 0x2f {
				j++ // intermediate bytes
			}

			if j >= len(data) {
				// Incomplete sequence at end of buffer — drop it.
				break
			}

			if data[j] == 'm' {
				// SGR (colors/styles) — keep it.
				result = append(result, data[i:j+1]...)
			}

			// All other CSI sequences are dropped.
			i = j

			continue
		}

		result = append(result, data[i])
	}

	return result
}

// stripCSI removes ANSI CSI (Control Sequence Introducer) sequences from data.
// CSI sequences start with ESC[ (0x1b 0x5b), followed by parameter bytes (0x30-0x3f),
// intermediate bytes (0x20-0x2f), and a final byte (0x40-0x7e).
// This filters out terminal responses (like cursor position reports) that the local
// terminal injects into stdin when the remote system sends queries.
//
//nolint:varnamelen
func stripCSI(data []byte) []byte {
	result := make([]byte, 0, len(data))

	for i := 0; i < len(data); i++ {
		if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '[' {
			// Skip ESC[
			i += 2

			// Skip parameter bytes (0x30-0x3f) and intermediate bytes (0x20-0x2f).
			for i < len(data) && data[i] >= 0x20 && data[i] < 0x40 {
				i++
			}

			// i now points at the final byte (0x40-0x7e); the outer loop's
			// i++ will advance past it on the next iteration.
			continue
		}

		result = append(result, data[i])
	}

	return result
}
