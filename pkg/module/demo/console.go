// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package demo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
	"github.com/BlindspotSoftware/dutctl/pkg/module/demo/internal/board"
)

func init() {
	module.Register(module.Record{
		ID:  "demo-console",
		New: func() module.Module { return &Console{} },
	})
}

// inputChunk bounds a read of the user's input: a few keys on a terminal, a
// bigger part of piped input.
const inputChunk = 256

// Console bridges the user's terminal to the demo board's serial console, as
// the serial module bridges a real board's. The board does the echo and the
// line editing, so the console is a raw one. It shows the raw console of
// module.Session with the cases a bridge has to handle: the user's input
// ends, and the output still works; the session is torn down, and a write
// fails; the board ends the session, while the user's input is still open,
// and Run ends the read on another goroutine with Stdin.Close.
type Console struct {
	// Expect is text that ends the console once the board printed it, the
	// last line of a boot for instance; unset, the console runs until the
	// user or the board's shell ends it.
	Expect string
}

// Ensure implementing the Module interface.
var _ module.Module = &Console{}

// Help describes how to use the console, and the configured expected text.
func (c *Console) Help() string {
	help := `Attach to the serial console of the simulated demo board.

ARGUMENTS:
	none

The board's shell knows help, version, echo <text> and exit; exit ends the
session, and so does Ctrl-A x. A board that is off stays silent: switch it
on, from a second terminal for instance, and watch it boot. What the board
printed while no console was attached is shown first.
`
	if c.Expect != "" {
		help += fmt.Sprintf("\nThe console ends by itself once the board printed %q.\n", c.Expect)
	}

	return help
}

// Init has nothing to set up.
func (c *Console) Init(_ context.Context) error {
	return nil
}

// Deinit has nothing to release.
func (c *Console) Deinit(_ context.Context) error {
	return nil
}

// Run bridges the console to the board until the user's input ends, the
// board's shell logs out, the board printed Expect or the session is torn
// down. It does not watch ctx:
// a cancelled command tears the session down, which ends the bridge.
func (c *Console) Run(ctx context.Context, s module.Session, args ...string) error {
	if len(args) > 0 {
		return fmt.Errorf("expected no arguments, got %d", len(args))
	}

	l := log.FromContext(ctx)
	con := s.OpenConsole(module.ConsoleOptions{Mode: module.ConsoleRaw})

	// The markers go to Stderr, so that a capture of Stdout holds what the
	// board sent and nothing else. The console is raw, hence CR LF.
	powered := demoBoard.On(board.Power)

	hint := "power it on to watch it boot"
	if powered {
		hint = "press Enter for a prompt"
	}

	_, err := fmt.Fprintf(con.Stderr, "--- Connected to the demo board, which is %s: %s ---\r\n", onOff(powered), hint)

	ending := "the input ended"
	if err == nil {
		ending, err = c.bridge(con)
	}

	if err == nil {
		_, err = io.WriteString(con.Stderr, "\r\n--- Connection closed ---\r\n")
	}

	if errors.Is(err, io.ErrClosedPipe) {
		// The user disconnected or the run was aborted: nobody is left to tell.
		l.Info("console ended: the session was torn down")

		return nil
	}

	if err != nil {
		return err
	}

	l.Info("console ended: " + ending)

	return nil
}

// bridge passes the user's keys to the board and the board's output to the
// user until the input ends, the board's shell logs out or the board printed
// Expect, and tells which it was. A read of the input blocks, while the
// board's output must be shown at once, so the input is read on a goroutine
// of its own. bridge stops it before it returns, as the console closes with
// Run: Stdin.Close ends a read that blocks, the input still open.
func (c *Console) bridge(con module.Console) (string, error) {
	keys := make(chan []byte)
	stop := make(chan struct{})
	pumped := make(chan struct{})

	go func() {
		defer close(pumped)

		pumpKeys(con.Stdin, keys, stop)
	}()

	defer func() {
		close(stop)

		_ = con.Stdin.Close()

		<-pumped
	}()

	// seen holds the end of the board's output that may start Expect, so
	// that Expect is found also when it spans two chunks of output.
	var seen string

	for {
		out, changed := demoBoard.TakeOutput()

		err := write(con.Stdout, out)
		if err != nil {
			return "", err
		}

		if c.Expect != "" {
			seen += string(out)
			if strings.Contains(seen, c.Expect) {
				return "the board printed the expected text", nil
			}

			seen = seen[max(0, len(seen)-len(c.Expect)+1):]
		}

		select {
		case chunk, ok := <-keys:
			if !ok {
				// Show what the last keys made the board print, then stop.
				out, _ = demoBoard.TakeOutput()

				return "the input ended", write(con.Stdout, out)
			}

			if demoBoard.TypeIn(chunk) {
				out, _ = demoBoard.TakeOutput()

				return "the board's shell logged out", write(con.Stdout, out)
			}
		case <-changed:
		}
	}
}

// pumpKeys sends what it reads from stdin to keys until the input ends or
// stop is closed, and closes keys when it returns.
func pumpKeys(stdin io.Reader, keys chan<- []byte, stop <-chan struct{}) {
	defer close(keys)

	for {
		buf := make([]byte, inputChunk)

		n, err := stdin.Read(buf)
		if n > 0 {
			select {
			case keys <- buf[:n]:
			case <-stop:
				return
			}
		}

		if err != nil {
			return // io.EOF: the input ended, or the console closed
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
