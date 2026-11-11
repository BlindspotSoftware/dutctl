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

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID:  "wrap-raw",
		New: func() module.Module { return &WrapRaw{} },
	})
}

// ctrlD is the key that ends the raw console of WrapRaw.
const ctrlD = 0x04

// WrapRaw wraps the raw console of module.Session: it shows the bytes the
// module receives for the keys a user presses, every key at once and as it
// is. It also shows that Print may be used while a console is open, and how
// a module takes the end of the user's input and a torn down session as the
// end of its console.
type WrapRaw struct{}

// Ensure implementing the Module interface.
var _ module.Module = &WrapRaw{}

// Help describes what the module shows.
func (w *WrapRaw) Help() string {
	return `Show the bytes your keys send, in a raw console.

ARGUMENTS:
	none

Every key arrives at once, as it is: Enter as 0d, Ctrl-C as 03, an arrow
key as an escape sequence. Each chunk the module receives is shown as one
line of hex bytes. A lone Ctrl-D ends the console.
`
}

// Init has nothing to set up.
func (w *WrapRaw) Init(_ context.Context) error {
	return nil
}

// Deinit has nothing to release.
func (w *WrapRaw) Deinit(_ context.Context) error {
	return nil
}

// Run shows every chunk of the user's input as one line of hex bytes, until
// a chunk is a lone Ctrl-D or the input ends. The instructions go through
// Print; the console's output is the bytes and nothing else.
func (w *WrapRaw) Run(_ context.Context, s module.Session, args ...string) error {
	if len(args) > 0 {
		return fmt.Errorf("expected no arguments, got %d", len(args))
	}

	con := s.OpenConsole(module.ConsoleOptions{Mode: module.ConsoleRaw})
	s.Println("Raw console: every key arrives at once, as it is; a lone Ctrl-D ends it.")

	buf := make([]byte, inputChunk)

	for {
		count, err := con.Stdin.Read(buf)
		if count > 0 {
			// A raw terminal does not turn a bare LF into a new line, hence CR LF.
			_, werr := io.WriteString(con.Stdout, hexBytes(buf[:count])+"\r\n")
			if errors.Is(werr, io.ErrClosedPipe) {
				return nil // the session was torn down: the console's end
			}

			if werr != nil {
				return werr
			}

			if count == 1 && buf[0] == ctrlD {
				return nil
			}
		}

		if errors.Is(err, io.EOF) {
			return nil // the user's input ended, or the session was torn down
		}

		if err != nil {
			return err
		}
	}
}

// hexBytes renders b as lowercase hex bytes separated by spaces.
func hexBytes(b []byte) string {
	hexes := make([]string, len(b))
	for i, c := range b {
		hexes[i] = fmt.Sprintf("%02x", c)
	}

	return strings.Join(hexes, " ")
}
