// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

import (
	"context"
	"io"
	"regexp"
	"time"
)

// port is the minimal serial-port surface the serial module needs. It is a
// subset of go.bug.st/serial.Port, so a serial.Port satisfies it directly,
// while tests can provide a fake.
type port interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	ResetInputBuffer() error
	Close() error
}

// matchWindow bounds the number of trailing output bytes the expect matcher
// keeps in memory. It limits how far back a single regex match may reach; it
// does NOT cap forwarded output (every byte is teed to the sink regardless).
// An expect pattern whose match would span more than matchWindow bytes cannot
// match. 64 KiB is ample for prompts and boot markers.
const matchWindow = 64 * 1024

// readChunk is the size of a single read from the serial port.
const readChunk = 4096

// engine drives a serial port for the scripted send/expect mode. It owns a
// rolling match buffer (never reset on newline) and tees every byte read from
// the port, processed by in, to an output sink (the client console in
// scripted mode).
//
// The output sink is an io.Writer so the byte-forwarding and matching logic
// stay independent of where the output ultimately goes.
type engine struct {
	p    port
	in   *inbound
	sink io.Writer
	buf  []byte
}

func newEngine(p port, in *inbound, sink io.Writer) *engine {
	return &engine{p: p, in: in, sink: sink, buf: make([]byte, 0, readChunk)}
}

// readUntil reads from the port, forwarding every byte to the sink, until
// pattern matches the accumulated output or ctx is done (global timeout /
// cancel).
// On a match, the matched span and everything before it is consumed from the
// buffer, so a later expect only sees subsequent output.
func (e *engine) readUntil(ctx context.Context, pattern *regexp.Regexp) error {
	readBuf := make([]byte, readChunk)

	for {
		// Check the whole buffer for a match FIRST, so output a prior step left
		// behind is honored, and so a match is tested before any trimming (it
		// can never be split at the trim boundary).
		if loc := pattern.FindIndex(e.buf); loc != nil {
			e.buf = e.buf[loc[1]:]

			return nil
		}

		// No match in the current buffer; bound it before reading more. Safe
		// here precisely because we just confirmed no completed match exists.
		e.capBuffer()

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// go.bug.st returns (0, nil) on a read timeout — keep waiting and let
		// the ctx check above enforce the overall deadline. Any real error
		// (port closed / device gone) ends the step.
		out, err := e.in.read(e.p, readBuf)

		werr := e.tee(out)
		if werr != nil {
			return werr
		}

		if err != nil {
			return err
		}
	}
}

// tee forwards out to the client, then feeds it to the matcher, which
// readUntil re-checks at the top of its next iteration.
func (e *engine) tee(out []byte) error {
	if len(out) == 0 {
		return nil
	}

	_, err := e.sink.Write(out)
	if err != nil {
		return err
	}

	e.buf = append(e.buf, out...)

	return nil
}

// write sends payload to the port, looping over short writes, then resets the
// match buffer so the next expect starts from output produced after the send.
func (e *engine) write(payload []byte) error {
	for len(payload) > 0 {
		n, err := e.p.Write(payload)
		if err != nil {
			return err
		}

		payload = payload[n:]
	}

	e.buf = e.buf[:0]

	return nil
}

// capBuffer keeps only the trailing matchWindow bytes so memory stays bounded
// during a long, non-matching stream. Trimming happens only after a failed
// match check, so it never drops bytes of a match that already completed.
func (e *engine) capBuffer() {
	if len(e.buf) > matchWindow {
		// copy(dst, src) is memmove-safe for the overlapping reslice; this
		// keeps the tail and lets the head be reused.
		e.buf = append(e.buf[:0], e.buf[len(e.buf)-matchWindow:]...)
	}
}

// pump reads from the port and forwards every byte to the sink until ctx is
// done (a normal end for a watch/drain, so it returns nil once what the
// character map still held back is written). On a real read error it returns
// the error, unless swallowReadErr is set — used by the post-send drain,
// where the device may have rebooted from the final send and the sequence
// has already completed successfully.
func (e *engine) pump(ctx context.Context, swallowReadErr bool) error {
	readBuf := make([]byte, readChunk)

	for {
		select {
		case <-ctx.Done():
			return e.in.release(e.sink)
		default:
		}

		out, err := e.in.read(e.p, readBuf)

		if len(out) > 0 {
			_, werr := e.sink.Write(out)
			if werr != nil {
				return werr
			}
		}

		if err != nil {
			if swallowReadErr {
				return nil
			}

			return err
		}
	}
}

// monitor streams serial output to the sink until ctx is done. It does no
// matching; the deadline or a client cancel is a normal end, so it returns nil
// in those cases and only fails on a real read error.
func (e *engine) monitor(ctx context.Context) error {
	return e.pump(ctx, false)
}

// drain forwards serial output for up to d, so the DUT's reply to a final send
// is visible before the run closes. It is best-effort: a read error (e.g. the
// device rebooted from that send) ends it without failing the run.
func (e *engine) drain(ctx context.Context, d time.Duration) error {
	drainCtx, cancel := context.WithTimeout(ctx, d)
	defer cancel()

	return e.pump(drainCtx, true)
}
