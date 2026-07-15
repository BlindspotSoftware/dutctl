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
// the port to an output sink (the client console in scripted mode).
//
// The output sink is an io.Writer so the byte-forwarding and matching logic
// stay independent of where the output ultimately goes.
type engine struct {
	p    port
	sink io.Writer
	buf  []byte

	// filter strips terminal escape sequences from output when true (disabled
	// by -keep-escapes); remainder carries a partial sequence split across reads.
	filter    bool
	remainder []byte
}

func newEngine(p port, sink io.Writer, filter bool) *engine {
	return &engine{p: p, sink: sink, buf: make([]byte, 0, readChunk), filter: filter}
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

		n, err := e.p.Read(readBuf)
		if n > 0 {
			out := e.clean(readBuf[:n])
			if len(out) > 0 {
				// Tee: forward to the client, then feed the matcher (re-checked
				// at the top of the next iteration).
				_, werr := e.sink.Write(out)
				if werr != nil {
					return werr
				}

				e.buf = append(e.buf, out...)
			}

			continue
		}

		// go.bug.st returns (0, nil) on a read timeout — keep waiting and let
		// the ctx check above enforce the overall deadline. Any real error
		// (port closed / device gone) ends the step.
		if err != nil {
			return err
		}
	}
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
// done (a normal end for a watch/drain, so it returns nil). On a real read
// error it returns the error, unless swallowReadErr is set — used by the
// post-send drain, where the device may have rebooted from the final send and
// the sequence has already completed successfully.
func (e *engine) pump(ctx context.Context, swallowReadErr bool) error {
	readBuf := make([]byte, readChunk)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		n, err := e.p.Read(readBuf)
		if n > 0 {
			out := e.clean(readBuf[:n])
			if len(out) > 0 {
				_, werr := e.sink.Write(out)
				if werr != nil {
					return werr
				}
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

// escByte is the ASCII escape that begins ANSI/VT escape sequences.
const escByte = 0x1b

// csiPrefixLen is the length of the CSI prefix "ESC[".
const csiPrefixLen = 2

// maxRemainder bounds the partial escape sequence carried across reads, so an
// unterminated sequence cannot grow the buffer without bound.
const maxRemainder = 4 * readChunk

// ANSI string-sequence terminators.
const (
	stByte  = 0x9c // C1 String Terminator
	belByte = 0x07 // BEL, also terminates an OSC
)

// CSI byte ranges (ECMA-48): parameter bytes 0x30–0x3f, intermediate bytes
// 0x20–0x2f, then a single final byte 0x40–0x7e.
const (
	csiParamLo = 0x30
	csiParamHi = 0x3f
	csiInterLo = 0x20
	csiInterHi = 0x2f
	csiFinalLo = 0x40
	csiFinalHi = 0x7e
)

// clean strips terminal escape sequences from a read chunk (unless filtering is
// disabled), reassembling sequences split across reads via remainder.
func (e *engine) clean(chunk []byte) []byte {
	if !e.filter {
		return chunk
	}

	if len(e.remainder) > 0 {
		chunk = append(e.remainder, chunk...)
		e.remainder = nil
	}

	out := stripEscapes(chunk, &e.remainder)

	if len(e.remainder) > maxRemainder {
		// An unterminated sequence this long is almost certainly junk; stop
		// carrying it so the buffer stays bounded. A terminator arriving in a
		// later chunk may then leak as a stray byte — acceptable for such
		// pathological input; preceding visible text is never lost.
		e.remainder = nil
	}

	return out
}

// stripEscapes removes ANSI/VT escape sequences from data, including SGR colour
// — the matched bytes then equal the visible text, so a prompt split by a colour
// change still matches. An incomplete sequence at the end of data is stored in
// remainder to be prepended to the next read.
func stripEscapes(data []byte, remainder *[]byte) []byte {
	result := make([]byte, 0, len(data))
	*remainder = nil

	for idx := 0; idx < len(data); idx++ {
		if data[idx] != escByte {
			result = append(result, data[idx])

			continue
		}

		end := seqEnd(data, idx)
		if end < 0 {
			// Incomplete sequence — carry it to the next read.
			*remainder = append([]byte(nil), data[idx:]...)

			return result
		}

		idx = end // skip the whole sequence; the loop's idx++ moves past it
	}

	return result
}

// seqEnd returns the index of the last byte of the escape sequence starting at
// the ESC byte data[escIdx], or -1 if the sequence is not complete within data.
func seqEnd(data []byte, escIdx int) int {
	if escIdx+1 >= len(data) {
		return -1 // lone ESC at end; the sequence may span reads
	}

	switch {
	case isStringSeqIntroducer(data[escIdx+1]):
		return stringSeqEnd(data, escIdx)
	case data[escIdx+1] == '[':
		return csiEnd(data, escIdx)
	default:
		return escIdx + 1 // two-byte escape, e.g. "ESC c"
	}
}

// isStringSeqIntroducer reports whether b (the byte after ESC) begins an ANSI
// string sequence: DCS (P), OSC (]), SOS (X), PM (^) or APC (_). These carry
// terminal queries/responses, never display content.
func isStringSeqIntroducer(b byte) bool {
	return b == 'P' || b == ']' || b == 'X' || b == '^' || b == '_'
}

// csiEnd returns the index of the final byte of the CSI sequence starting at
// data[escIdx] ("ESC["), or -1 if it is not complete within data.
func csiEnd(data []byte, escIdx int) int {
	pos := escIdx + csiPrefixLen
	for pos < len(data) && data[pos] >= csiParamLo && data[pos] <= csiParamHi {
		pos++ // parameter bytes
	}

	for pos < len(data) && data[pos] >= csiInterLo && data[pos] <= csiInterHi {
		pos++ // intermediate bytes
	}

	if pos >= len(data) {
		return -1 // final byte not yet read
	}

	if data[pos] < csiFinalLo || data[pos] > csiFinalHi {
		// Not a valid CSI final byte — e.g. a new ESC interrupting the sequence
		// (a terminal aborts an in-progress CSI on a fresh ESC) or a stray
		// control byte. Abort and re-scan this byte: drop "ESC[..." up to but
		// excluding it (the caller does idx = end, then idx++ lands on data[pos]).
		return pos - 1
	}

	return pos // final byte (0x40–0x7e)
}

// stringSeqEnd returns the index of the last byte of the ANSI string sequence
// starting at data[escIdx], or -1 if it is not terminated within data. It ends
// at ST ("ESC \" or 0x9c); an OSC may also end at BEL.
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
		}
	}

	return -1
}
