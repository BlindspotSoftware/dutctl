// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package chanio

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"
)

func TestNewChanWriter(t *testing.T) {
	// Test with valid channel
	ch := make(chan []byte)
	writer, err := NewChanWriter(ch, nil)

	if err != nil {
		t.Fatalf("NewChanWriter() returned an error: %v", err)
	}

	if writer.ch == nil {
		t.Errorf("NewChanWriter() returned a ChanWriter with a nil channel")
	}

	// Test with nil channel
	writer, err = NewChanWriter(nil, nil)

	if err == nil {
		t.Fatalf("NewChanWriter() did not return an error for nil channel")
	}

	if writer != nil {
		t.Errorf("NewChanWriter() returned a non-nil ChanWriter for nil channel")
	}
}

func TestNewChanReader(t *testing.T) {
	// Test with valid channel
	ch := make(chan []byte)
	reader, err := NewChanReader(ch, nil, nil)

	if err != nil {
		t.Fatalf("NewChanReader() returned an error: %v", err)
	}

	if reader.ch == nil {
		t.Errorf("NewChanReader() returned a ChanReader with a nil channel")
	}

	if reader.buf == nil {
		t.Errorf("NewChanReader() returned a ChanReader with a nil buffer")
	}

	// Test with nil channel
	reader, err = NewChanReader(nil, nil, nil)

	if err == nil {
		t.Fatalf("NewChanReader() did not return an error for nil channel")
	}

	if reader != nil {
		t.Errorf("NewChanReader() returned a non-nil ChanReader for nil channel")
	}
}

func TestChanReader_Read(t *testing.T) {
	tests := []struct {
		name       string
		bufferInit []byte   // Initial buffer content
		channelIn  [][]byte // Data to send through the channel
		readSize   int      // Size of the slice passed to Read
		want       []byte   // Expected data to be read
		wantN      int      // Expected number of bytes read
		wantErr    error    // Expected error
	}{
		{
			name:       "read from buffer",
			bufferInit: []byte("hello"),
			channelIn:  [][]byte{},
			readSize:   5,
			want:       []byte("hello"),
			wantN:      5,
			wantErr:    nil,
		},
		{
			name:       "read from channel",
			bufferInit: []byte{},
			channelIn:  [][]byte{[]byte("world")},
			readSize:   5,
			want:       []byte("world"),
			wantN:      5,
			wantErr:    nil,
		},
		{
			// An empty channel message must be skipped, not surfaced as (0, nil):
			// Read loops until it has real data (or EOF), which the io.Reader
			// contract requires for a non-empty request.
			name:       "skip empty message before data",
			bufferInit: []byte{},
			channelIn:  [][]byte{{}, []byte("world")},
			readSize:   5,
			want:       []byte("world"),
			wantN:      5,
			wantErr:    nil,
		},
		{
			// A partial buffer is served on its own: the channel is not read to
			// fill the request up, which would wait for a message to arrive.
			name:       "partial buffer is served alone",
			bufferInit: []byte("he"),
			channelIn:  [][]byte{[]byte("llo")},
			readSize:   5,
			want:       []byte("he"),
			wantN:      2,
			wantErr:    nil,
		},
		{
			name:       "buffer larger than read size",
			bufferInit: []byte("hello"),
			channelIn:  [][]byte{},
			readSize:   3,
			want:       []byte("hel"),
			wantN:      3,
			wantErr:    nil,
		},
		{
			name:       "EOF when channel closed",
			bufferInit: []byte{},
			channelIn:  [][]byte{}, // Indicate channel is closed immediately
			readSize:   5,
			want:       []byte{},
			wantN:      0,
			wantErr:    io.EOF,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := make(chan []byte, len(tt.channelIn))
			for _, b := range tt.channelIn {
				ch <- b
			}

			close(ch) // Simulate closing the channel after sending all data

			r := &ChanReader{
				ch:  ch,
				buf: tt.bufferInit,
			}

			p := make([]byte, tt.readSize)

			gotN, err := r.Read(p)
			if err != tt.wantErr {
				t.Errorf("Read() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotN != tt.wantN {
				t.Errorf("Read() gotN = %v, want %v", gotN, tt.wantN)
			}
			if !reflect.DeepEqual(p[:gotN], tt.want) {
				t.Errorf("Read() got = %v, want %v", p[:gotN], tt.want)
			}
		})
	}
}

func TestChanReaderWithReadAll(t *testing.T) {
	tests := []struct {
		name       string
		bufferInit []byte
		channelIn  [][]byte
		want       []byte
	}{
		{
			name:       "read all from buffer and channel",
			bufferInit: []byte("hello"),
			channelIn:  [][]byte{[]byte(" "), []byte("world")},
			want:       []byte("hello world"),
		},
		{
			name:       "read all from channel only",
			bufferInit: []byte{},
			channelIn:  [][]byte{[]byte("hello"), []byte(" "), []byte("world")},
			want:       []byte("hello world"),
		},
		{
			name:       "read all from buffer only",
			bufferInit: []byte("hello world"),
			channelIn:  [][]byte{},
			want:       []byte("hello world"),
		},
		{
			name:       "empty buffer and channel",
			bufferInit: []byte{},
			channelIn:  [][]byte{},
			want:       []byte{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := make(chan []byte, len(tt.channelIn))
			for _, b := range tt.channelIn {
				ch <- b
			}
			close(ch)

			r := &ChanReader{
				ch:  ch,
				buf: tt.bufferInit,
			}

			got, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("ReadAll() got = %s, want = %s", got, tt.want)
			}
		})
	}
}

// TestChanReader_ReadServesBufferFirst pins two things about bytes buffered
// from an earlier, larger message: a Read returns them on their own, without
// waiting for another message, and a message that overflows the destination
// is buffered for the next Read, never dropped.
func TestChanReader_ReadServesBufferFirst(t *testing.T) {
	ch := make(chan []byte, 1)
	ch <- []byte("llobig") // the channel delivers 6 bytes in one chunk
	close(ch)

	r := &ChanReader{
		ch:  ch,
		buf: []byte("he"), // 2 bytes already buffered
	}

	p := make([]byte, 5)

	// The buffered bytes come alone: the channel is not consulted while the
	// buffer has bytes, although the request has room for more.
	n, err := r.Read(p)
	if err != nil || string(p[:n]) != "he" {
		t.Fatalf("first Read() = %q, %v; want %q, nil (the buffered bytes on their own)", p[:n], err, "he")
	}

	// A 4-byte request takes 4 bytes of the 6-byte message; the overflow is
	// buffered ...
	n, err = r.Read(p[:4])
	if err != nil || string(p[:n]) != "llob" {
		t.Fatalf("second Read() = %q, %v; want %q, nil", p[:n], err, "llob")
	}

	// ... and served next, alone, before the EOF of the closed channel.
	n, err = r.Read(p)
	if err != nil || string(p[:n]) != "ig" {
		t.Fatalf("third Read() = %q, %v; want %q, nil (the overflow, not dropped)", p[:n], err, "ig")
	}

	if n, err = r.Read(p); n != 0 || !errors.Is(err, io.EOF) {
		t.Errorf("fourth Read() = %d, %v; want 0, io.EOF", n, err)
	}
}

// A zero-length request returns at once with nothing, as io.Reader asks; it
// neither waits for a message nor takes one.
func TestChanReader_ZeroLengthRead(t *testing.T) {
	ch := make(chan []byte, 1)
	ch <- []byte("data")

	r, err := NewChanReader(ch, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if n, err := r.Read(nil); n != 0 || err != nil {
		t.Fatalf("Read(nil) = %d, %v; want 0, nil", n, err)
	}

	p := make([]byte, 8)

	if n, err := r.Read(p); err != nil || string(p[:n]) != "data" {
		t.Errorf("Read after a zero-length one = %q, %v; want the message untouched", p[:n], err)
	}
}

// TestChanReader_LeftoverDoesNotWait: the tail of a message the previous Read
// could not take is returned by the next Read at once, although no further
// message arrives. For a console that is the difference between the end of a
// paste showing up now and showing up at the next keystroke.
func TestChanReader_LeftoverDoesNotWait(t *testing.T) {
	ch := make(chan []byte) // unbuffered and never fed after the one message

	r, err := NewChanReader(ch, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	go func() { ch <- []byte("0123456789") }()

	p := make([]byte, 6)

	n, err := r.Read(p)
	if err != nil || string(p[:n]) != "012345" {
		t.Fatalf("first Read() = %q, %v; want %q, nil", p[:n], err, "012345")
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		n, err = r.Read(p)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second Read() waited for another message instead of returning the leftover")
	}

	if err != nil || string(p[:n]) != "6789" {
		t.Errorf("second Read() = %q, %v; want %q, nil", p[:n], err, "6789")
	}
}

// TestChanReaderDoneUnblocksRead verifies that closing the done channel unblocks
// a Read that is waiting on a source channel which is never fed or closed,
// returning io.EOF; a buffered remainder is served by the Read before, never
// together with the EOF. This is what unwedges a module parked reading stdin
// when its session is torn down.
func TestChanReaderDoneUnblocksRead(t *testing.T) {
	ch := make(chan []byte)     // never fed, never closed
	done := make(chan struct{}) //nolint:varnamelen // done is the conventional name

	r, err := NewChanReader(ch, done, nil)
	if err != nil {
		t.Fatalf("NewChanReader() error = %v", err)
	}

	readDone := make(chan struct{})

	var (
		n      int
		readEr error
	)

	go func() {
		n, readEr = r.Read(make([]byte, 8))
		close(readDone)
	}()

	// The Read must be blocked: nothing is on ch and done is still open.
	select {
	case <-readDone:
		t.Fatal("Read returned before done was closed (should have blocked)")
	case <-time.After(50 * time.Millisecond):
	}

	close(done)

	select {
	case <-readDone:
		if !errors.Is(readEr, io.EOF) {
			t.Errorf("Read after done err = %v, want io.EOF", readEr)
		}

		if n != 0 {
			t.Errorf("Read after done n = %d, want 0", n)
		}
	case <-time.After(time.Second):
		t.Fatal("Read did not return after done was closed")
	}
}

// TestChanWriterDoneUnblocksWrite verifies that closing the done channel unblocks
// a Write that is waiting on a source channel with no receiver, returning
// io.ErrClosedPipe. This is what unwedges a module parked writing stdout/stderr
// when its session is torn down.
func TestChanWriterDoneUnblocksWrite(t *testing.T) {
	ch := make(chan []byte)     // no receiver
	done := make(chan struct{}) //nolint:varnamelen // done is the conventional name

	w, err := NewChanWriter(ch, done)
	if err != nil {
		t.Fatalf("NewChanWriter() error = %v", err)
	}

	writeDone := make(chan struct{})

	var (
		n       int
		writeEr error
	)

	go func() {
		n, writeEr = w.Write([]byte("payload"))
		close(writeDone)
	}()

	// The Write must be blocked: no receiver on ch and done is still open.
	select {
	case <-writeDone:
		t.Fatal("Write returned before done was closed (should have blocked)")
	case <-time.After(50 * time.Millisecond):
	}

	close(done)

	select {
	case <-writeDone:
		if !errors.Is(writeEr, io.ErrClosedPipe) {
			t.Errorf("Write after done err = %v, want io.ErrClosedPipe", writeEr)
		}

		if n != 0 {
			t.Errorf("Write after done n = %d, want 0", n)
		}
	case <-time.After(time.Second):
		t.Fatal("Write did not return after done was closed")
	}
}
