// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

// inbound processes the bytes the port delivers before they reach the client,
// the same way in every mode: the monitor and the scripted sequence strip the
// terminal escape sequences, so output and expect patterns see the visible
// text, while the interactive console passes them on for the user's terminal
// to interpret. It is stateful, since a sequence may be split across reads,
// so each run uses one of its own.
type inbound struct {
	// strip removes terminal escape sequences; -keep-escapes turns it off,
	// and the interactive console never strips.
	strip bool
	// remainder carries a partial escape sequence from one read to the next.
	remainder []byte
}

func newInbound(strip bool) *inbound {
	return &inbound{strip: strip}
}

// apply returns what of chunk is to be shown.
func (in *inbound) apply(chunk []byte) []byte {
	if !in.strip {
		return chunk
	}

	if len(in.remainder) > 0 {
		chunk = append(in.remainder, chunk...)
		in.remainder = nil
	}

	out := stripEscapes(chunk, &in.remainder)

	if len(in.remainder) > maxRemainder {
		// A control string this long with no terminator and no escape in it is
		// almost certainly junk; stop carrying it so the buffer stays bounded.
		// What was read after its start is dropped with it, and a terminator
		// arriving later leaks as a stray byte — acceptable for such
		// pathological input; the visible text before it is never lost.
		in.remainder = nil
	}

	return out
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

// nF escape sequences (ECMA-35), "ESC ( B" for instance: after the ESC come
// intermediate bytes 0x20–0x2f, then a single final byte 0x30–0x7e.
const (
	nfFinalLo = 0x30
	nfFinalHi = 0x7e
)

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
// A control byte after the ESC cannot continue a sequence: a terminal executes
// it and forgets the ESC, so the sequence is the lone ESC and the control byte
// is re-scanned as data.
func seqEnd(data []byte, escIdx int) int {
	if escIdx+1 >= len(data) {
		return -1 // lone ESC at end; the sequence may span reads
	}

	switch {
	case data[escIdx+1] < csiInterLo:
		return escIdx
	case isStringSeqIntroducer(data[escIdx+1]):
		return stringSeqEnd(data, escIdx)
	case data[escIdx+1] == '[':
		return csiEnd(data, escIdx)
	case data[escIdx+1] >= csiInterLo && data[escIdx+1] <= csiInterHi:
		return nfEnd(data, escIdx)
	default:
		return escIdx + 1 // two-byte escape, e.g. "ESC c"
	}
}

// nfEnd returns the index of the final byte of the nF escape sequence starting
// at data[escIdx] (ESC followed by an intermediate byte), or -1 if it is not
// complete within data. Charset designations such as "ESC ( B", which a getty
// and the Linux console send, are of this shape; taken for a two-byte escape
// their final byte would leak into the output.
func nfEnd(data []byte, escIdx int) int {
	pos := escIdx + 1
	for pos < len(data) && data[pos] >= csiInterLo && data[pos] <= csiInterHi {
		pos++ // intermediate bytes
	}

	if pos >= len(data) {
		return -1 // final byte not yet read
	}

	if data[pos] < nfFinalLo || data[pos] > nfFinalHi {
		// Not a final byte: the sequence is aborted, as a terminal aborts it.
		// Drop what was read of it and re-scan this byte, like csiEnd.
		return pos - 1
	}

	return pos
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
// at ST ("ESC \" or 0x9c); an OSC may also end at BEL. Any other ESC ends the
// string as well, as it does in a terminal, and begins a sequence of its own:
// the string ends before it, and the ESC is re-scanned.
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

			return pos - 1
		}
	}

	return -1
}
