// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package chanio

import (
	"bytes"
	"io"
	"reflect"
	"testing"
)

func TestNewChanWriter(t *testing.T) {
	// Test with valid channel
	ch := make(chan []byte)
	writer, err := NewChanWriter(ch)

	if err != nil {
		t.Fatalf("NewChanWriter() returned an error: %v", err)
	}

	if writer.ch == nil {
		t.Errorf("NewChanWriter() returned a ChanWriter with a nil channel")
	}

	// Test with nil channel
	writer, err = NewChanWriter(nil)

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
	reader, err := NewChanReader(ch, nil)

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
	reader, err = NewChanReader(nil, nil)

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
			name:       "partial buffer, partial channel",
			bufferInit: []byte("he"),
			channelIn:  [][]byte{[]byte("llo")},
			readSize:   5,
			want:       []byte("hello"),
			wantN:      5,
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

// TestChanReader_ReadStraddleOverflow is a regression test: when a single Read
// is served partly from the internal buffer and partly from the channel, and
// the channel chunk is larger than the space left in the destination, the
// overflow must be buffered for the next Read, never dropped.
func TestChanReader_ReadStraddleOverflow(t *testing.T) {
	ch := make(chan []byte, 1)
	ch <- []byte("llobig") // the channel delivers 6 bytes in one chunk
	close(ch)

	r := &ChanReader{
		ch:  ch,
		buf: []byte("he"), // 2 bytes already buffered
	}

	// First Read into a 5-byte slice: 2 bytes from the buffer leave room for 3
	// channel bytes ("llo"); the remaining 3 ("big") overflow and must be kept.
	p := make([]byte, 5)

	n, err := r.Read(p)
	if err != nil {
		t.Fatalf("first Read() error = %v", err)
	}

	if got := string(p[:n]); got != "hello" {
		t.Fatalf("first Read() = %q, want %q", got, "hello")
	}

	// Draining the rest must yield the buffered overflow, so no byte is lost.
	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}

	if got := "hello" + string(rest); got != "hellobig" {
		t.Errorf("total read = %q, want %q (bytes dropped when a read straddled buffer and channel)", got, "hellobig")
	}
}
