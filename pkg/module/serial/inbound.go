// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

import (
	"fmt"
	"io"
	"strings"
)

// charMap is a set of substitutions for single bytes the DUT sends, named as
// picocom names them for its --imap option, which tio, minicom, PuTTY and
// Tera Term offer under names of their own with the same meaning. A DUT that
// ends its lines in a bare LF needs lfcrlf to display right on a raw
// terminal, which does not return the cursor to the start of the line by
// itself; the Linux console, U-Boot and a getty send CR LF, so most DUTs
// need no map at all.
type charMap struct {
	crlf   bool // CR becomes LF
	crcrlf bool // a CR that no LF follows becomes CR LF
	igncr  bool // CR is dropped
	lfcr   bool // LF becomes CR
	lfcrlf bool // a LF that no CR precedes becomes CR LF
	ignlf  bool // LF is dropped
	bsdel  bool // BS becomes DEL
	delbs  bool // DEL becomes BS
}

// charMapNames lists the names parseCharMap accepts, for messages.
const charMapNames = "crlf, crcrlf, igncr, lfcr, lfcrlf, ignlf, bsdel or delbs"

// parseCharMap builds a charMap from its names, case-insensitively; an empty
// list is the empty map.
//
//nolint:cyclop // a flat name switch
func parseCharMap(names []string) (charMap, error) {
	var maps charMap

	for _, name := range names {
		switch strings.ToLower(name) {
		case "crlf":
			maps.crlf = true
		case "crcrlf":
			maps.crcrlf = true
		case "igncr":
			maps.igncr = true
		case "lfcr":
			maps.lfcr = true
		case "lfcrlf":
			maps.lfcrlf = true
		case "ignlf":
			maps.ignlf = true
		case "bsdel":
			maps.bsdel = true
		case "delbs":
			maps.delbs = true
		default:
			return charMap{}, fmt.Errorf("unknown character map %q: want %s", name, charMapNames)
		}
	}

	return maps, nil
}

// The bytes the maps act on.
const (
	bsByte  = 0x08
	crByte  = '\r'
	lfByte  = '\n'
	delByte = 0x7f
)

// inbound processes the bytes the port delivers before they reach the client,
// the same way in every mode: first the device's character map, then, for
// the monitor and the scripted sequence, the escape filter, so output and
// expect patterns see the visible text; the interactive console passes the
// sequences on for the user's terminal to interpret. It is stateful, since a
// sequence or a CR LF pair may be split across reads, so each run uses one of
// its own.
type inbound struct {
	// maps is the device's character map.
	maps charMap
	// prevCR records that the last byte emitted was a CR, across chunks, so
	// lfcrlf leaves the LF of a CR LF pair alone; a CR another map dropped
	// does not count, so its LF is bare.
	prevCR bool
	// heldCR records a CR at the end of a chunk that crcrlf holds back until
	// the next byte tells whether a LF follows; flush releases it on a pause.
	heldCR bool

	// strip removes terminal escape sequences; -keep-escapes turns it off,
	// and the interactive console never strips.
	strip bool
	// remainder carries a partial escape sequence from one read to the next.
	remainder []byte
}

func newInbound(maps charMap, strip bool) *inbound {
	return &inbound{maps: maps, strip: strip}
}

// read reads the port once into buf and returns what of it is to be shown,
// with the port's error, if any. A read that timed out, (0, nil), is a pause
// in the device's output and releases what the character map held back.
func (in *inbound) read(serialPort port, buf []byte) ([]byte, error) {
	n, err := serialPort.Read(buf)
	if n == 0 && err == nil {
		return in.flush(), nil
	}

	return in.apply(buf[:n]), err
}

// apply returns what of chunk is to be shown.
func (in *inbound) apply(chunk []byte) []byte {
	if in.maps != (charMap{}) || in.heldCR {
		chunk = in.mapChars(chunk)
	}

	return in.stripSequences(chunk)
}

// flush returns what a pause in the input releases: a CR held back for
// crcrlf, which no LF followed within the pause, as CR LF. The bytes take the
// same way as mapped ones, through the escape filter, so a sequence pending
// there ends on them as a terminal would end it.
func (in *inbound) flush() []byte {
	if !in.heldCR {
		return nil
	}

	in.heldCR = false

	released := []byte{crByte, lfByte}
	in.noteTail(released)

	return in.stripSequences(released)
}

// release writes what a pause releases to w, if anything: a loop calls it
// when it ends, so a CR held back for crcrlf is not lost with the run.
func (in *inbound) release(w io.Writer) error {
	out := in.flush()
	if len(out) == 0 {
		return nil
	}

	_, err := w.Write(out)

	return err
}

// stripSequences removes the terminal escape sequences from chunk while
// stripping is on, reassembling a sequence split across reads.
func (in *inbound) stripSequences(chunk []byte) []byte {
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

// mapChars applies the character map to chunk.
func (in *inbound) mapChars(chunk []byte) []byte {
	out := make([]byte, 0, len(chunk)+len(chunk)/8+2) //nolint:mnd // room for a few added bytes

	for idx, b := range chunk {
		out = in.releaseCR(out, b)
		in.noteTail(out)

		switch b {
		case crByte:
			out = in.mapCR(out, chunk[idx+1:])
		case lfByte:
			out = in.mapLF(out)
		case bsByte:
			if in.maps.bsdel {
				b = delByte
			}

			out = append(out, b)
		case delByte:
			if in.maps.delbs {
				b = bsByte
			}

			out = append(out, b)
		default:
			out = append(out, b)
		}
	}

	in.noteTail(out)

	return out
}

// noteTail records whether the last byte emitted so far is a CR. While
// nothing was emitted for this chunk yet, the previous chunk's tail stands.
func (in *inbound) noteTail(out []byte) {
	if len(out) > 0 {
		in.prevCR = out[len(out)-1] == crByte
	}
}

// releaseCR emits a CR held back at the end of the previous chunk now that
// next, the byte after it, is known: as CR LF unless next is the LF.
func (in *inbound) releaseCR(out []byte, next byte) []byte {
	if !in.heldCR {
		return out
	}

	in.heldCR = false

	out = append(out, crByte)

	if next != lfByte {
		out = append(out, lfByte)
	}

	return out
}

// mapCR emits a CR as the map says; rest is what follows it in the chunk, so
// crcrlf can tell whether a LF does. A CR at the end of the chunk is held
// back for that until the next chunk or a pause.
func (in *inbound) mapCR(out []byte, rest []byte) []byte {
	switch {
	case in.maps.igncr:
		return out
	case in.maps.crlf:
		return append(out, lfByte)
	case !in.maps.crcrlf:
		return append(out, crByte)
	case len(rest) == 0:
		in.heldCR = true

		return out
	case rest[0] == lfByte:
		return append(out, crByte)
	default:
		return append(out, crByte, lfByte)
	}
}

// mapLF emits a LF as the map says; lfcrlf adds the CR only when the previous
// byte was not one.
func (in *inbound) mapLF(out []byte) []byte {
	switch {
	case in.maps.ignlf:
		return out
	case in.maps.lfcr:
		return append(out, crByte)
	case in.maps.lfcrlf && !in.prevCR:
		return append(out, crByte, lfByte)
	default:
		return append(out, lfByte)
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
