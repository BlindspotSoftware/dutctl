// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"testing"
	"time"
)

// fakePort is a test double for the port interface.
//
// reads is a queue of results returned by successive Read calls; an empty
// element simulates a (0, nil) read timeout. Once the queue is exhausted, Read
// returns (0, readErr) — a nil readErr therefore models a port that has gone
// quiet (perpetual timeout), used to drive timeout tests.
type fakePort struct {
	reads      [][]byte
	readErr    error
	written    []byte
	closed     bool
	resetCount int
}

func (f *fakePort) Read(p []byte) (int, error) {
	if len(f.reads) == 0 {
		return 0, f.readErr
	}

	chunk := f.reads[0]
	f.reads = f.reads[1:]

	if len(chunk) == 0 {
		return 0, nil // simulated timeout tick
	}

	n := copy(p, chunk)
	if n < len(chunk) { // push back the remainder for the next Read
		f.reads = append([][]byte{chunk[n:]}, f.reads...)
	}

	return n, nil
}

func (f *fakePort) Write(p []byte) (int, error) {
	f.written = append(f.written, p...)

	return len(p), nil
}

func (f *fakePort) ResetInputBuffer() error { f.resetCount++; return nil }
func (f *fakePort) Close() error            { f.closed = true; return nil }

var errExhausted = errors.New("fake port: reads exhausted")

func TestEngineReadUntilMatchesAcrossReads(t *testing.T) {
	fp := &fakePort{
		reads:   [][]byte{[]byte("foo "), []byte("ba"), []byte("r baz")},
		readErr: errExhausted,
	}
	sink := &bytes.Buffer{}
	eng := newEngine(fp, sink, escKeepAll)

	if err := eng.readUntil(context.Background(), regexp.MustCompile("bar")); err != nil {
		t.Fatalf("readUntil: %v", err)
	}

	if got, want := sink.String(), "foo bar baz"; got != want {
		t.Errorf("sink = %q, want %q", got, want)
	}

	if got, want := string(eng.buf), " baz"; got != want {
		t.Errorf("leftover buf = %q, want %q (match must be consumed through its end)", got, want)
	}
}

func TestEngineSequentialExpectsConsumeBuffer(t *testing.T) {
	// "foobar baz" arrives in a single read; the first expect must consume
	// through its match and the second must find the already-buffered tail
	// WITHOUT needing another read.
	fp := &fakePort{reads: [][]byte{[]byte("foobar baz")}, readErr: errExhausted}
	eng := newEngine(fp, &bytes.Buffer{}, escKeepAll)

	if err := eng.readUntil(context.Background(), regexp.MustCompile("bar")); err != nil {
		t.Fatalf("first expect: %v", err)
	}

	if err := eng.readUntil(context.Background(), regexp.MustCompile("baz")); err != nil {
		t.Fatalf("second expect (should match buffered tail without reading): %v", err)
	}
}

func TestEngineReadUntilTimeout(t *testing.T) {
	// One non-matching chunk, then perpetual (0, nil) timeouts.
	fp := &fakePort{reads: [][]byte{[]byte("nope")}, readErr: nil}
	eng := newEngine(fp, &bytes.Buffer{}, escKeepAll)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	err := eng.readUntil(ctx, regexp.MustCompile("never"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestEngineReadUntilPortError(t *testing.T) {
	fp := &fakePort{reads: nil, readErr: errExhausted}
	eng := newEngine(fp, &bytes.Buffer{}, escKeepAll)

	err := eng.readUntil(context.Background(), regexp.MustCompile("x"))
	if !errors.Is(err, errExhausted) {
		t.Errorf("err = %v, want errExhausted", err)
	}
}

func TestEngineWriteLoopsAndResetsBuffer(t *testing.T) {
	fp := &fakePort{}
	eng := newEngine(fp, &bytes.Buffer{}, escKeepAll)
	eng.buf = []byte("stale data from a prior expect")

	if err := eng.write([]byte("reboot\r")); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got, want := string(fp.written), "reboot\r"; got != want {
		t.Errorf("written = %q, want %q", got, want)
	}

	if len(eng.buf) != 0 {
		t.Errorf("buf not reset after write: %q", eng.buf)
	}
}

func TestEngineCapBufferKeepsTail(t *testing.T) {
	eng := newEngine(&fakePort{}, &bytes.Buffer{}, escKeepAll)
	eng.buf = append(bytes.Repeat([]byte("a"), matchWindow+100), []byte("MARK")...)

	eng.capBuffer()

	if len(eng.buf) != matchWindow {
		t.Errorf("len(buf) = %d, want %d", len(eng.buf), matchWindow)
	}

	if !bytes.HasSuffix(eng.buf, []byte("MARK")) {
		t.Errorf("capBuffer dropped the most recent bytes; want suffix %q", "MARK")
	}
}

func TestEngineMonitorForwardsUntilCtxDone(t *testing.T) {
	fp := &fakePort{reads: [][]byte{[]byte("boot log line\n")}} // then quiet (0,nil)
	sink := &bytes.Buffer{}
	eng := newEngine(fp, sink, escKeepAll)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	if err := eng.monitor(ctx); err != nil {
		t.Fatalf("monitor: %v", err)
	}

	if got, want := sink.String(), "boot log line\n"; got != want {
		t.Errorf("sink = %q, want %q", got, want)
	}
}

func TestEngineMonitorReturnsReadError(t *testing.T) {
	fp := &fakePort{reads: nil, readErr: errExhausted}
	eng := newEngine(fp, &bytes.Buffer{}, escKeepAll)

	if err := eng.monitor(context.Background()); !errors.Is(err, errExhausted) {
		t.Errorf("monitor err = %v, want errExhausted", err)
	}
}

func TestEngineDrainForwardsBestEffort(t *testing.T) {
	// Drain forwards available output and returns nil even when the read then
	// errors (e.g. the device rebooted from the final send).
	fp := &fakePort{reads: [][]byte{[]byte("Rebooting...\n")}, readErr: errExhausted}
	sink := &bytes.Buffer{}
	eng := newEngine(fp, sink, escKeepAll)

	if err := eng.drain(context.Background(), 50*time.Millisecond); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if got, want := sink.String(), "Rebooting...\n"; got != want {
		t.Errorf("sink = %q, want %q", got, want)
	}
}

func TestStripEscapes(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantRem string
	}{
		{"plain text", "hello world", "hello world", ""},
		{"sgr colour", "a\x1b[31mred\x1b[0mb", "aredb", ""},
		{"csi clear screen", "x\x1b[2Jy", "xy", ""},
		{"csi cursor query", "x\x1b[6ny", "xy", ""},
		{"osc with bel", "a\x1b]0;title\x07b", "ab", ""},
		{"dcs with st", "a\x1bPq;data\x1b\\b", "ab", ""},
		{"two-byte escape", "a\x1bcb", "ab", ""},
		{"csi interrupted by esc", "a\x1b[1;\x1b[0mb", "ab", ""},
		{"csi no params then esc", "\x1b[\x1b[31mRED", "RED", ""},
		{"incomplete csi carried over", "a\x1b[31", "a", "\x1b[31"},
		{"lone esc at end carried over", "a\x1b", "a", "\x1b"},
		{"incomplete osc carried over", "a\x1b]0;ti", "a", "\x1b]0;ti"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rem []byte

			got := stripEscapes([]byte(tt.in), &rem, false)
			if string(got) != tt.want {
				t.Errorf("stripEscapes(%q) = %q, want %q", tt.in, got, tt.want)
			}

			if string(rem) != tt.wantRem {
				t.Errorf("remainder = %q, want %q", rem, tt.wantRem)
			}
		})
	}
}

func TestStripEscapesKeepDisplay(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantRem string
	}{
		{"sgr colour kept", "a\x1b[31mred\x1b[0mb", "a\x1b[31mred\x1b[0mb", ""},
		{"erase and cursor moves kept", "\x1b[2J\x1b[H\x1b[10;5Hx", "\x1b[2J\x1b[H\x1b[10;5Hx", ""},
		{"alternate screen kept", "\x1b[?1049hx\x1b[?1049l", "\x1b[?1049hx\x1b[?1049l", ""},
		{"cursor save and restore kept", "\x1b7x\x1b8", "\x1b7x\x1b8", ""},
		{"cursor position query kept", "\x1b[6n", "\x1b[6n", ""},
		{"device attributes query kept", "\x1b[c", "\x1b[c", ""},
		{"osc title removed", "a\x1b]0;title\x07b", "ab", ""},
		{"osc clipboard removed", "a\x1b]52;c;ZXZpbA==\x1b\\b", "ab", ""},
		{"dcs removed", "a\x1bPq;data\x1b\\b", "ab", ""},
		{"window resize removed", "a\x1b[8;50;200tb", "ab", ""},
		{"title report removed", "a\x1b[21tb", "ab", ""},
		{"csi cut short removed, next kept", "a\x1b[1;\x1b[0mb", "a\x1b[0mb", ""},
		{"incomplete csi carried over", "a\x1b[31", "a", "\x1b[31"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rem []byte

			got := stripEscapes([]byte(tt.in), &rem, true)
			if string(got) != tt.want {
				t.Errorf("stripEscapes(%q, keepDisplay) = %q, want %q", tt.in, got, tt.want)
			}

			if string(rem) != tt.wantRem {
				t.Errorf("remainder = %q, want %q", rem, tt.wantRem)
			}
		})
	}
}

func TestEngineCleanReassemblesSplitSequence(t *testing.T) {
	eng := newEngine(&fakePort{}, &bytes.Buffer{}, escStripAll)

	out1 := eng.clean([]byte("a\x1b[3")) // incomplete CSI, tail carried over
	out2 := eng.clean([]byte("2mb"))     // completes the sequence

	if got := string(out1) + string(out2); got != "ab" {
		t.Errorf("split filter = %q, want %q", got, "ab")
	}
}

func TestEngineCleanDisabledPassesThrough(t *testing.T) {
	eng := newEngine(&fakePort{}, &bytes.Buffer{}, escKeepAll)

	in := []byte("a\x1b[31mb")
	if got := eng.clean(in); string(got) != string(in) {
		t.Errorf("clean with filtering off = %q, want unchanged %q", got, in)
	}
}

func TestEngineReadUntilFiltersEscapes(t *testing.T) {
	fp := &fakePort{reads: [][]byte{[]byte("\x1b[32mlogin:\x1b[0m ")}, readErr: errExhausted}
	sink := &bytes.Buffer{}
	eng := newEngine(fp, sink, escStripAll)

	if err := eng.readUntil(context.Background(), regexp.MustCompile("login:")); err != nil {
		t.Fatalf("readUntil: %v", err)
	}

	if got, want := sink.String(), "login: "; got != want {
		t.Errorf("sink = %q, want %q (escapes stripped)", got, want)
	}
}
