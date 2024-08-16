package main

import (
	"bytes"
	"io"
	"reflect"
	"testing"
)

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

			r := &chanReader{
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

			r := &chanReader{
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
