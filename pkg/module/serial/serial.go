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

// Serial is a module that forwards the serial output of a connected DUT to the dutctl client.
// It is non-interactive and does not support stdin yet.
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
The serial connection is read-only and does not support stdin yet.
If a regex is provided, the module will wait for the regex to match on the serial output, 
then exit with success. If no expect string is provided, the module will read from the serial port
until it is terminated by a signal (e.g. Ctrl-C).
The  expect string supports regular expressions according to [1].
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

	port, err := s.openPort()
	if err != nil {
		return err
	}
	defer port.Close()

	return nil
}

func (s *Serial) Deinit() error {
	log.Println("serial module: Deinit called")

	return nil
}

//nolint:cyclop,funlen,gocognit
func (s *Serial) Run(ctx context.Context, session module.Session, args ...string) error {
	log.Println("serial module: Run called")

	if err := s.evalArgs(args); err != nil {
		return err
	}

	port, err := s.openPort()
	if err != nil {
		return err
	}
	defer port.Close()

	log.Printf("serial module: connected to %s at %d baud", s.Port, s.Baud)
	session.Print(fmt.Sprintf("--- Connected to %s at %d baud ---\n", s.Port, s.Baud))

	var cancel context.CancelFunc

	if s.timeout > 0 {
		log.Printf("serial module: setting timeout of %s", s.timeout)
		ctx, cancel = context.WithTimeout(ctx, s.timeout)

		defer cancel()
	}

	const bufferSize = 4096
	readBuffer := make([]byte, bufferSize)
	lineBuffer := &bytes.Buffer{}

	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				session.Print("\n--- Timeout reached, no match found ---\n")

				return fmt.Errorf("timeout of %s reached, pattern %q not found", s.timeout, s.expect)
			}

			session.Print("\n--- Connection closed ---\n")

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

			// Process the data read character by character
			for i := range sbytes {
				b := readBuffer[i]
				lineBuffer.WriteByte(b)

				// If we reach a newline or a buffer limit, process the line
				if b == '\n' || lineBuffer.Len() >= 1024 {
					line := lineBuffer.String()
					session.Print(line)

					// Check for regex match if we have one
					if s.expect != nil && s.expect.MatchString(line) {
						session.Print("\n--- Pattern matched, connection closed ---\n")

						return nil // Success - pattern found
					}

					lineBuffer.Reset()
				}
			}
		}
	}
}

func (s *Serial) evalArgs(args []string) error {
	fs := flag.NewFlagSet("serial", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // Suppress default error output
	fs.DurationVar(&s.timeout, "t", 0, "timeout duration (e.g. 3m, 30s)")

	if err := fs.Parse(args); err != nil {
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
