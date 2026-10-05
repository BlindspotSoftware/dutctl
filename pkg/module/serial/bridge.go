// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// inputChunk is the size of a single read from the console's input. Keystrokes
// arrive one or a few bytes at a time, and a paste in bigger chunks; 256 keeps
// a paste in few port writes without buffering it noticeably.
const inputChunk = 256

// bridge connects the port to the console in both directions until the run
// ends, and tears the bridge down so no port access outlives it. Bytes pass
// verbatim both ways: the client terminal is raw, so what the user types,
// Enter as CR and Ctrl-C included, is what the DUT's line discipline expects,
// and what the DUT sends, escape sequences included, is what the user's
// terminal interprets. Nothing is filtered or matched; a scripted run goes
// through the engine instead.
//
// The run ends normally, with nil, when ctx is done (the -t bound or a
// cancellation, as in monitor mode), when the session is gone (the console's
// writers fail with io.ErrClosedPipe), or when the user's input ended and
// drain elapsed since, so a piped input such as printf 'ls\r' ends the run
// by itself once the DUT had time to reply. It fails on a port error.
//
// The markers go to the console's Stderr, so a capture of Stdout holds the
// DUT's bytes and nothing else, and they end in CR LF because the client
// terminal is raw and would stair-step a bare LF.
func bridge(ctx context.Context, serialPort port, con module.Console, drain time.Duration, name string, baud int) error {
	_, _ = fmt.Fprintf(con.Stderr, "--- Connected to %s at %d baud ---\r\n", name, baud)

	// The input pump runs on its own goroutine, since a Read on Stdin and a Read
	// on the port both block. stopped is closed when it returned, and writeErr
	// is read only after that.
	stopped := make(chan struct{})

	var writeErr error

	go func() {
		defer close(stopped)

		writeErr = forwardInput(con.Stdin, serialPort)
	}()

	err := forwardOutput(ctx, serialPort, con.Stdout, stopped, drain)

	// Teardown, on every path: end the input, which returns a Read parked on
	// Stdin with io.EOF, and wait for the input pump to return. Only then may
	// the caller close the port; serial.Port.Write has no close check, so a
	// Close during an in-flight Write would race it.
	_ = con.Stdin.Close()

	<-stopped

	_, _ = io.WriteString(con.Stderr, "\r\n--- Connection closed ---\r\n")

	switch {
	case errors.Is(err, io.ErrClosedPipe):
		// The session is gone, so there is no client to report to; the run
		// is being torn down and ends like a cancelled one.
		return nil
	case err != nil:
		return fmt.Errorf("interactive: %w", err)
	case writeErr != nil:
		return fmt.Errorf("interactive: write to port: %w", writeErr)
	default:
		return nil
	}
}

// forwardInput copies the console's input to the port until the input ends.
// Every read error ends it, since io.EOF covers all the ways the input ends:
// the user's input at its end, the console closed, the session torn down, and
// the bridge's own Stdin.Close on teardown. It returns the port write error
// that stopped it, if any; the end of the input is not an error.
func forwardInput(stdin io.Reader, serialPort port) error {
	buf := make([]byte, inputChunk)

	var err error

	for err == nil {
		var n int

		n, err = stdin.Read(buf)
		if n > 0 {
			werr := writeAll(serialPort, buf[:n])
			if werr != nil {
				return werr
			}
		}
	}

	return nil
}

// writeAll writes data to the port, looping over short writes.
func writeAll(serialPort port, data []byte) error {
	for len(data) > 0 {
		n, err := serialPort.Write(data)
		if err != nil {
			return err
		}

		data = data[n:]
	}

	return nil
}

// forwardOutput copies the port's output to stdout, verbatim, and returns nil
// when ctx is done or when drain has elapsed since inputStopped was closed.
// It returns a port read error as it is, and a stdout write error as it is,
// so the caller can tell a gone session (io.ErrClosedPipe) from a failure.
//
// The port's read timeout, after which Read returns (0, nil), is what makes
// the loop re-check its end conditions.
func forwardOutput(ctx context.Context, serialPort port, stdout io.Writer, inputStopped <-chan struct{}, drain time.Duration) error {
	buf := make([]byte, readChunk)

	// drained fires once drain elapsed after the input ended; nil until then,
	// and a nil channel never fires. The one-shot timer behind it needs no
	// Stop: since Go 1.23 a timer nothing refers to is collected.
	var drained <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-inputStopped:
			// Keep forwarding for drain after the input ended, so the DUT's
			// reply to the last input is still shown. The timer is started
			// once, and the closed channel, ready on every iteration, is
			// taken out of the select so the timer's case is the next to fire.
			drained = time.After(drain)
			inputStopped = nil
		case <-drained:
			return nil
		default:
		}

		n, err := serialPort.Read(buf)
		if n > 0 {
			_, werr := stdout.Write(buf[:n])
			if werr != nil {
				return werr
			}
		}

		if err != nil {
			return err
		}
	}
}
