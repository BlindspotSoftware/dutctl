// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package chanio provides a way to use channels as io.Reader and io.Writer.
package chanio

import (
	"errors"
	"io"
	"log"
)

// Read implements io.Reader.
//
// Use NewChanReader to obtain a new ChanReader.
type ChanReader struct {
	ch  <-chan []byte
	buf []byte // Buffer to store excess bytes
}

// NewChanReader returns a new ChanReader reading from ch. The provided channel must not be nil.
func NewChanReader(ch <-chan []byte) (*ChanReader, error) {
	if ch == nil {
		return nil, errors.New("cannot create a ChanReader with a nil channel")
	}

	return &ChanReader{
		ch:  ch,
		buf: make([]byte, 0),
	}, nil
}

// Read reads up to len(bytes) bytes into bytes. It returns the number of bytes
// read and any error encountered.
func (r *ChanReader) Read(bytes []byte) (int, error) {
	log.Printf("Channel Reader: Read called with %d bytes\n", len(bytes))
	// If there's enough data in the buffer, use it and return early.
	if len(r.buf) >= len(bytes) {
		n := copy(bytes, r.buf)
		r.buf = r.buf[n:] // Adjust the buffer
		log.Printf("Channel Reader: Returning early (internal buffer >= read buffer), %d bytes from buffer\n", n)

		return n, nil
	}

	log.Printf("Channel Reader: no early return, continue to read from buffer and channel\n")

	var nBuf, nChan int
	// If the buffer is not empty but contains some data, start by filling bytes with it.
	if len(r.buf) > 0 {
		log.Printf("Channel Reader: Reading from internal buffer\n")

		nBuf = copy(bytes, r.buf)
		r.buf = r.buf[nBuf:] // Adjust the buffer

		if nBuf == len(bytes) {
			// If the buffer fulfilled the bytes, return early
			log.Printf("Channel Reader: Returning early (internal buffer = read buffer), %d bytes from buffer\n", nBuf)

			return nBuf, nil
		}
	}

	log.Printf("Channel Reader: Continue reading from channel\n")

	// Read from the channel if the buffer is empty or insufficient
	chanBytes, ok := <-r.ch
	if !ok {
		log.Printf("Channel Reader: Channel closed returning EOF")

		return nBuf, io.EOF // Return any remaining buffer content before EOF
	}

	log.Printf("Channel Reader: Channel read %d bytes. Continue calculating total read count\n", len(chanBytes))

	// Calculate the total bytes to copy to bytes, considering any existing content from the buffer
	totalNeeded := len(bytes) - len(r.buf)
	nChan = copy(bytes[nBuf:], chanBytes)

	// If there are excess bytes, append them to the buffer
	if totalNeeded < len(chanBytes) {
		r.buf = append(r.buf, chanBytes[totalNeeded:]...)
	}

	log.Printf("Channel Reader: Read %d bytes from internal buffer and %d bytes from channel\n", nBuf, nChan)

	return nBuf + nChan, nil
}

// Write implements io.Writer.
//
// Use NewChanWriter to obtain a new ChanWriter.
type ChanWriter struct {
	ch chan<- []byte
}

// NewChanWriter returns a new ChanWriter writing to ch. The provided channel must not be nil.
func NewChanWriter(ch chan<- []byte) (*ChanWriter, error) {
	if ch == nil {
		return nil, errors.New("cannot create a ChanWriter with a nil channel")
	}

	return &ChanWriter{
		ch: ch,
	}, nil
}

// Write writes len(bytes) bytes from bytes to the underlying data stream.
// It returns the number of bytes written from bytes
// and any error encountered that caused the write to stop early.
func (w *ChanWriter) Write(bytes []byte) (int, error) {
	chanBytes := make([]byte, len(bytes))
	copy(chanBytes, bytes)
	w.ch <- chanBytes

	return len(bytes), nil
}
