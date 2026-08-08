// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"sync"
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

// reconnectInterval is the delay between attempts to reopen the serial device
// after it has disappeared (e.g. an FTDI chip that powers down with the DUT).
const reconnectInterval = 500 * time.Millisecond

// deviceLossGraceDefault is how long a quiet port is tolerated before the module
// checks whether the device node still exists. A read timeout and a removed
// device look identical (both yield a data-less read), so the module waits this
// long, then confirms via the node check before treating quiet as loss. Kept
// short so a real disconnect is noticed quickly, but long enough not to stat the
// node on every idle read.
const deviceLossGraceDefault = 2 * time.Second

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

	// mu guards p, which is swapped when the device disappears and is reopened
	// (see reconnect). The read loop copies p under mu, then reads without the
	// lock; the interactive stdin pump writes through portWrite under mu. The
	// lock is never held across a blocking read, so the two cannot deadlock.
	mu sync.Mutex

	// reopen reopens the device after loss; a nil reopen disables auto-reconnect
	// (used by the direct engine unit tests and the post-send drain). present
	// reports whether the device node still exists; grace is the quiet period
	// tolerated before the node is checked. All three are set together via
	// withReconnect.
	reopen  func() (port, error)
	present func() bool
	grace   time.Duration
}

func newEngine(p port, sink io.Writer, filter bool) *engine {
	return &engine{p: p, sink: sink, buf: make([]byte, 0, readChunk), filter: filter}
}

// withReconnect enables auto-reconnect on device loss for this engine. reopen
// re-opens the device, present reports whether its node still exists, and grace
// is how long a quiet port is tolerated before the node is checked. Run wires
// this up for the streaming and expect modes; it is left unset for the direct
// engine unit tests and for the post-send drain, which stay non-reconnecting.
func (e *engine) withReconnect(reopen func() (port, error), present func() bool, grace time.Duration) {
	e.reopen = reopen
	e.present = present
	e.grace = grace
}

// currentPort returns the live port handle under the lock, so a caller can read
// or write it without racing a reconnect that swaps it.
func (e *engine) currentPort() port {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.p
}

// setPort installs a freshly opened port after a reconnect.
func (e *engine) setPort(p port) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.p = p
}

// closeCurrent closes the live port (best-effort) and clears the handle, so a
// dropped session is fully discarded and the stdin pump stops writing to it.
func (e *engine) closeCurrent() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.p != nil {
		_ = e.p.Close()
		e.p = nil
	}
}

// portWrite writes data to the live port under the lock. It drops the data when
// the device is gone (handle nil during a reconnect) rather than blocking.
func (e *engine) portWrite(data []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.p == nil {
		return nil // device gone; drop input until it reconnects
	}

	for len(data) > 0 {
		n, err := e.p.Write(data)
		if err != nil {
			return err
		}

		data = data[n:]
	}

	return nil
}

// marker writes a status line straight to the output sink (used for the
// disconnect/reconnect banners, which must appear even mid-stream).
func (e *engine) marker(msg string) {
	_, _ = e.sink.Write([]byte(msg))
}

// read reads one chunk from the live port. The handle is copied under the lock
// so a concurrent reconnect is safe, while the blocking read runs without the
// lock so it never stalls an interactive stdin write.
func (e *engine) read(buf []byte) (int, error) {
	p := e.currentPort()
	if p == nil {
		return 0, nil
	}

	return p.Read(buf)
}

// handleReadLoss decides whether a data-less read reflects a vanished device
// and, if reconnect is enabled, waits for the device to return. It reports
// whether a reconnect happened (the caller should retry the read) and returns
// ctx.Err() if the wait was cancelled. With reconnect disabled it is a no-op
// returning (false, nil), so non-reconnecting callers keep their old behaviour.
func (e *engine) handleReadLoss(ctx context.Context, readErr error, lastData *time.Time) (bool, error) {
	if e.reopen == nil {
		return false, nil
	}

	// A hard read error is an outright device error → reconnect immediately. A
	// quiet read (0, nil) only counts as loss once the device node has been gone
	// for the grace period: an idle read and a removed device look identical, so
	// the node check disambiguates a merely quiet console from a vanished one.
	if readErr == nil {
		if e.present == nil || e.present() || time.Since(*lastData) <= e.grace {
			return false, nil
		}
	}

	err := e.reconnect(ctx)
	if err != nil {
		return false, err
	}

	*lastData = time.Now()

	return true, nil
}

// reconnect closes the vanished port and retries opening it until the device
// reappears, the deadline fires, or the session is cancelled. It mirrors tio's
// auto-reconnect so a DUT power-cycle that drops the FTDI device does not end
// the session. On return either a fresh port is installed (nil error) or ctx is
// done (its error).
func (e *engine) reconnect(ctx context.Context) error {
	e.closeCurrent()

	e.marker("\n--- Serial device disconnected, waiting to reconnect ---\n")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		p, err := e.reopen()
		if err == nil {
			_ = p.ResetInputBuffer()
			e.setPort(p)
			e.marker("\n--- Serial device reconnected ---\n")

			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(reconnectInterval):
		}
	}
}

// readUntil reads from the port, forwarding every byte to the sink, until
// pattern matches the accumulated output or ctx is done (global timeout /
// cancel).
// On a match, the matched span and everything before it is consumed from the
// buffer, so a later expect only sees subsequent output.
//
//nolint:cyclop // match-check, resilient read, and device-loss handling share one loop; splitting hurts clarity
func (e *engine) readUntil(ctx context.Context, pattern *regexp.Regexp) error {
	readBuf := make([]byte, readChunk)
	lastData := time.Now()

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

		n, err := e.read(readBuf)
		if n > 0 {
			lastData = time.Now()

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

		// No data. go.bug.st returns (0, nil) on a read timeout; a real error
		// means the device is gone. With reconnect enabled, wait for the device
		// to return and keep matching against the preserved buffer. Otherwise a
		// hard error ends the step, while a timeout just keeps waiting for the
		// deadline (checked at the top of the loop).
		lost, rerr := e.handleReadLoss(ctx, err, &lastData)
		if rerr != nil {
			return rerr
		}

		if lost {
			continue
		}

		if err != nil {
			return err
		}
	}
}

// write sends payload to the port, looping over short writes, then resets the
// match buffer so the next expect starts from output produced after the send.
func (e *engine) write(payload []byte) error {
	err := e.portWrite(payload)
	if err != nil {
		return err
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
// done (a normal end for a watch/drain, so it returns nil).
//
// With reconnectOnLoss set, a vanished device is handled transparently: the pump
// waits for it to reappear and keeps streaming (used by monitor and interactive
// mode). With it clear, a read error ends the pump — returning the error, or nil
// when swallowReadErr is set, as the post-send drain does since the device may
// have rebooted from that final send.
//
//nolint:cyclop // read/emit/reconnect/idle dispatch in one streaming loop; the branch count is inherent
func (e *engine) pump(ctx context.Context, reconnectOnLoss, swallowReadErr bool) error {
	readBuf := make([]byte, readChunk)
	lastData := time.Now()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		n, err := e.read(readBuf)
		if n > 0 {
			lastData = time.Now()

			out := e.clean(readBuf[:n])
			if len(out) > 0 {
				_, werr := e.sink.Write(out)
				if werr != nil {
					return werr
				}
			}
		}

		if reconnectOnLoss && e.reopen != nil {
			// handleReadLoss reconnects on a hard error or a vanished node; on
			// ctx cancellation it returns an error, which for a pump is a normal
			// end. A benign idle read falls through and the loop continues.
			_, rerr := e.handleReadLoss(ctx, err, &lastData)
			if rerr != nil {
				return nil //nolint:nilerr // ctx cancelled mid-reconnect is a normal end for a pump
			}

			continue
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
// in those cases. With reconnect wired up (see Run) it survives device loss.
func (e *engine) monitor(ctx context.Context) error {
	return e.pump(ctx, true, false)
}

// drain forwards serial output for up to d, so the DUT's reply to a final send
// is visible before the run closes. It is best-effort: a read error (e.g. the
// device rebooted from that send) ends it without failing the run, and it does
// not reconnect — the sequence has already completed.
func (e *engine) drain(ctx context.Context, d time.Duration) error {
	drainCtx, cancel := context.WithTimeout(ctx, d)
	defer cancel()

	return e.pump(drainCtx, false, true)
}

// stdinChunk is the read size for the interactive stdin pump.
const stdinChunk = 256

// interactive bridges the client console and the serial port: it forwards
// client keystrokes (stdin) to the port and streams port output to the sink,
// until ctx is done. Device loss is handled by the pump's reconnect path (wired
// via withReconnect). It returns nil on ctx cancellation, a normal end that the
// caller maps to the right result.
func (e *engine) interactive(ctx context.Context, stdin io.Reader) error {
	// done stops the stdin pump from writing to the port once interactive
	// returns and the port is about to be closed.
	done := make(chan struct{})
	defer close(done)

	go e.stdinPump(stdin, done)

	return e.pump(ctx, true, false)
}

// stdinPump forwards client keystrokes to the serial port until stdin reaches
// EOF (the agent closes stdin on session teardown). It is the only goroutine
// that may briefly outlive interactive(); it touches no match state and drops
// writes once done is closed, so it can neither race the read loop nor write to
// a closed port.
func (e *engine) stdinPump(stdin io.Reader, done <-chan struct{}) {
	buf := make([]byte, stdinChunk)

	for {
		n, err := stdin.Read(buf)
		if n > 0 {
			select {
			case <-done:
				return // interactive exited — do not write to the closing port.
			default:
				_ = e.portWrite(buf[:n])
			}
		}

		if err != nil {
			return // stdin EOF on teardown, or a read error.
		}
	}
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
	*remainder = nil

	// Most serial output carries no escape sequence; when there is none, return
	// the input untouched. The result is consumed synchronously by the caller
	// (written to the sink or copied into the match buffer) before the next read,
	// so aliasing data is safe.
	if bytes.IndexByte(data, escByte) < 0 {
		return data
	}

	result := make([]byte, 0, len(data))

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
