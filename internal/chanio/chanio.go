// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package chanio provides a way to use channels as io.Reader and io.Writer.
package chanio

import (
	"errors"
	"io"
	"log/slog"
)

// ChanReader implements io.Reader that reads from a channel of byte slices.
// Use NewChanReader to obtain a new ChanReader.
type ChanReader struct {
	ch  <-chan []byte
	buf []byte // Buffer to store excess bytes
	log *slog.Logger
}

// NewChanReader returns a new ChanReader reading from ch. The provided channel
// must not be nil. Reads are traced at debug level through logger; pass an
// already-scoped logger (its scope is used as-is) or nil for slog.Default().
func NewChanReader(ch <-chan []byte, logger *slog.Logger) (*ChanReader, error) {
	if ch == nil {
		return nil, errors.New("cannot create a ChanReader with a nil channel")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &ChanReader{
		ch:  ch,
		buf: make([]byte, 0),
		log: logger,
	}, nil
}

// logger returns the reader's logger, falling back to the default if it was
// constructed directly (e.g. in a test) rather than via NewChanReader.
func (r *ChanReader) logger() *slog.Logger {
	if r.log != nil {
		return r.log
	}

	return slog.Default()
}

// Read reads up to len(bytes) bytes into bytes. It returns the number of bytes
// read and any error encountered.
func (r *ChanReader) Read(bytes []byte) (int, error) {
	r.logger().Debug("chan read", "want", len(bytes))

	// If there's enough data in the buffer, use it and return early.
	if len(r.buf) >= len(bytes) {
		n := copy(bytes, r.buf)
		r.buf = r.buf[n:] // Adjust the buffer
		r.logger().Debug("chan read served from buffer", "n", n)

		return n, nil
	}

	var nBuf, nChan int
	// If the buffer is not empty but contains some data, start by filling bytes with it.
	if len(r.buf) > 0 {
		nBuf = copy(bytes, r.buf)
		r.buf = r.buf[nBuf:] // Adjust the buffer

		if nBuf == len(bytes) {
			// If the buffer fulfilled the bytes, return early
			r.logger().Debug("chan read served from buffer", "n", nBuf)

			return nBuf, nil
		}
	}

	// Read from the channel if the buffer is empty or insufficient
	chanBytes, ok := <-r.ch
	if !ok {
		r.logger().Debug("chan read EOF: channel closed", "buffered", nBuf)

		return nBuf, io.EOF // Return any remaining buffer content before EOF
	}

	// Fill the space left in bytes after the buffer copy with data from the
	// channel. copy caps at that remaining space (len(bytes)-nBuf), so nChan is
	// exactly how many channel bytes were consumed.
	nChan = copy(bytes[nBuf:], chanBytes)

	// Buffer whatever did not fit for the next Read.
	if nChan < len(chanBytes) {
		r.buf = append(r.buf, chanBytes[nChan:]...)
	}

	r.logger().Debug("chan read complete", "from_buffer", nBuf, "from_channel", nChan)

	return nBuf + nChan, nil
}

// ChanWriter implements io.Writer that writes to a channel of byte slices.
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
