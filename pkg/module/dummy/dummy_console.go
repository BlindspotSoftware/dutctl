// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dummy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID:  "dummy-console",
		New: func() module.Module { return &Console{} },
	})
}

// Console echoes the user's input through a console. It demonstrates the
// OpenConsole method of module.Session in both of its modes and lets a user
// see what their client delivers to a module: the raw demo echoes bytes as
// received, the hex demo shows which bytes a key produces, and the line demo
// echoes text lines.
type Console struct{}

// Ensure implementing the Module interface.
var _ module.Module = &Console{}

const (
	demoRaw  = "raw"
	demoHex  = "hex"
	demoLine = "line"

	// ctrlD is the byte a terminal sends for Ctrl-D. In a raw console it is
	// not an end of input, so the demo takes a lone one as its end.
	ctrlD = 0x04

	// chunkSize bounds a raw read; a chunk that is exactly one Ctrl-D ends
	// the demo, so the bound does not matter for correctness.
	chunkSize = 256

	rawBanner    = "--- dummy-console raw: bytes are echoed as received; a lone Ctrl-D ends ---\r\n"
	hexBanner    = "--- dummy-console hex: each chunk of input is shown as hex bytes; a lone Ctrl-D ends ---\r\n"
	lineGreeting = "Hello from dummy-console (line mode). Type a line; 'quit' or the end of input ends.\n"
)

func (d *Console) Help() string {
	return `This dummy module echoes the user's input through a console and shows
what the client delivers to a module.

ARGUMENTS:
	[raw|hex|line]

  raw   (default) opens a raw console and echoes every byte as it was
        received. A lone Ctrl-D ends the demo.
  hex   opens a raw console and prints each chunk of input as one line of
        hex bytes, so you see exactly which bytes a key produces.
        A lone Ctrl-D ends the demo.
  line  opens a line console and echoes each line as "> " followed by the
        line. The line "quit" or the end of input ends the demo.`
}

func (d *Console) Init(_ context.Context) error {
	return nil
}

func (d *Console) Deinit(_ context.Context) error {
	return nil
}

// Run does not watch ctx: when the command is aborted the session is torn
// down, Stdin then reports io.EOF, and the demo ends through its normal path.
func (d *Console) Run(_ context.Context, s module.Session, args ...string) error {
	demo, err := demoFromArgs(args)
	if err != nil {
		return err
	}

	switch demo {
	case demoHex:
		return runBytes(s, hexBanner, hexLine)
	case demoLine:
		return runLine(s)
	default:
		return runBytes(s, rawBanner, rawEcho)
	}
}

func demoFromArgs(args []string) (string, error) {
	if len(args) == 0 {
		return demoRaw, nil
	}

	if len(args) > 1 {
		return "", fmt.Errorf("expected at most one argument, got %d", len(args))
	}

	switch args[0] {
	case demoRaw, demoHex, demoLine:
		return args[0], nil
	default:
		return "", fmt.Errorf("unknown demo %q, want raw, hex or line", args[0])
	}
}

// runBytes opens a raw console and writes render(chunk) for every chunk read
// until a chunk that is exactly one Ctrl-D, the end of the input, or a failed
// write ends it. render returns nothing when a chunk is not to be shown.
func runBytes(s module.Session, banner string, render func(chunk []byte) []byte) error {
	con := s.OpenConsole(module.ConsoleOptions{Mode: module.ConsoleRaw})

	_, err := con.Stderr.Write([]byte(banner))
	if err != nil {
		return err
	}

	var buf [chunkSize]byte

	for {
		n, err := con.Stdin.Read(buf[:])
		if n > 0 {
			chunk := buf[:n]

			werr := write(con.Stdout, render(chunk))
			if werr != nil {
				return werr
			}

			if endsDemo(chunk) {
				return nil
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil // the user's input ended
			}

			return err
		}
	}
}

// endsDemo reports whether chunk is the lone Ctrl-D that ends a raw demo. A
// Ctrl-D among other bytes is input like any other.
func endsDemo(chunk []byte) bool {
	return len(chunk) == 1 && chunk[0] == ctrlD
}

// rawEcho renders a chunk as it was received. The Ctrl-D that ends the demo
// is a command to it, not input, so there is nothing to echo for it.
func rawEcho(chunk []byte) []byte {
	if endsDemo(chunk) {
		return nil
	}

	return chunk
}

// hexLine renders a chunk as one line of lowercase hex bytes, ending in CR LF
// because a raw terminal does not turn a bare LF into a new line.
func hexLine(chunk []byte) []byte {
	var b strings.Builder

	for i, c := range chunk {
		if i > 0 {
			b.WriteByte(' ')
		}

		fmt.Fprintf(&b, "%02x", c)
	}

	b.WriteString("\r\n")

	return []byte(b.String())
}

// runLine opens a line console and echoes every line until the line "quit",
// the end of the input, or a failed write ends it. A final line without a
// newline, as a pipe delivers it, is echoed with one so that the output ends
// on a line of its own.
func runLine(s module.Session) error {
	con := s.OpenConsole(module.ConsoleOptions{})

	_, err := con.Stdout.Write([]byte(lineGreeting))
	if err != nil {
		return err
	}

	reader := bufio.NewReader(con.Stdin)

	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			if strings.TrimSuffix(line, "\n") == "quit" {
				return nil
			}

			werr := write(con.Stdout, []byte("> "+strings.TrimSuffix(line, "\n")+"\n"))
			if werr != nil {
				return werr
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil // the user's input ended
			}

			return err
		}
	}
}

// write writes b to w unless there is nothing to write.
func write(w io.Writer, b []byte) error {
	if len(b) == 0 {
		return nil
	}

	_, err := w.Write(b)

	return err
}
