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
	ch   <-chan []byte
	done <-chan struct{}
	buf  []byte // Buffer to store excess bytes
	log  *slog.Logger
}

// NewChanReader returns a new ChanReader reading from ch. The provided channel
// must not be nil. If done is non-nil, a Read blocked on the channel unblocks
// with io.EOF once done is closed, so a caller torn down mid-read is not wedged
// forever on a channel whose sender is gone; pass nil for a reader ended only by
// the source channel closing. Reads are traced at debug level through logger;
// pass an already-scoped logger (its scope is used as-is) or nil for
// slog.Default().
func NewChanReader(ch <-chan []byte, done <-chan struct{}, logger *slog.Logger) (*ChanReader, error) {
	if ch == nil {
		return nil, errors.New("cannot create a ChanReader with a nil channel")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &ChanReader{
		ch:   ch,
		done: done,
		buf:  make([]byte, 0),
		log:  logger,
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

// Read reads up to len(bytes) bytes into bytes: from the remainder of an
// earlier channel message while one is buffered, otherwise from the next
// channel message. It returns the number of bytes read and any error
// encountered.
//
// It returns io.EOF once nothing is buffered and the channel or the done
// channel (if any) is closed. Bytes from a channel message that do not fit are
// buffered and served by the next Read on their own, without waiting for
// another message. Empty channel messages are skipped rather than surfaced as
// (0, nil), so a request for a non-empty buffer never returns (0, nil); a
// zero-length request returns (0, nil) at once.
func (r *ChanReader) Read(bytes []byte) (int, error) {
	r.logger().Debug("chan read", "want", len(bytes))

	if len(bytes) == 0 {
		return 0, nil
	}

	// A partial buffer is returned as it is, as io.Reader allows: waiting for
	// the next message to fill bytes up would hold the tail of a message back
	// until another one arrives, which for a console's input is the next key.
	if len(r.buf) > 0 {
		n := copy(bytes, r.buf)
		r.buf = r.buf[n:]
		r.logger().Debug("chan read served from buffer", "n", n)

		return n, nil
	}

	// Read from the channel until there is at least one byte to return (or the
	// channel closes). Skipping empty messages keeps Read from returning (0, nil)
	// for a non-empty request, which would violate the io.Reader contract.
	for {
		select {
		case <-r.done:
			// The session was torn down: unblock a reader parked here (e.g. a
			// module in io.ReadAll on stdin, which never closes) by reporting
			// EOF. A nil done channel is never selected, preserving plain
			// channel-read semantics.
			r.logger().Debug("chan read EOF: done signalled")

			return 0, io.EOF
		case chanBytes, ok := <-r.ch:
			if !ok {
				r.logger().Debug("chan read EOF: channel closed")

				return 0, io.EOF
			}

			// copy caps at len(bytes), so taken is exactly how many of the
			// message's bytes it took; the rest is buffered for the next Read.
			taken := copy(bytes, chanBytes)
			if taken < len(chanBytes) {
				r.buf = append(r.buf, chanBytes[taken:]...)
			}

			if taken == 0 {
				// An empty message: wait for real data rather than returning
				// (0, nil).
				continue
			}

			r.logger().Debug("chan read complete", "n", taken)

			return taken, nil
		}
	}
}

// ChanWriter implements io.Writer that writes to a channel of byte slices.
// Use NewChanWriter to obtain a new ChanWriter.
type ChanWriter struct {
	ch   chan<- []byte
	done <-chan struct{}
}

// NewChanWriter returns a new ChanWriter writing to ch. The provided channel must
// not be nil. If done is non-nil, a Write blocked on the channel unblocks with
// io.ErrClosedPipe once done is closed, so a caller torn down mid-write is not
// wedged forever on a channel whose receiver is gone; pass nil for a writer that
// only ever completes by a receiver taking the message.
func NewChanWriter(ch chan<- []byte, done <-chan struct{}) (*ChanWriter, error) {
	if ch == nil {
		return nil, errors.New("cannot create a ChanWriter with a nil channel")
	}

	return &ChanWriter{
		ch:   ch,
		done: done,
	}, nil
}

// Write copies bytes and sends the copy on the underlying channel, returning
// len(bytes) and a nil error once a receiver takes it.
//
// If the writer's done channel is closed while the send is blocked, Write returns
// (0, io.ErrClosedPipe) instead of blocking forever on a channel whose receiver
// is gone. Write must not be called after the underlying channel is closed: the
// send is otherwise unguarded, so writing to a closed channel panics. In this
// package the broker owns the channel lifetime and never closes it while a module
// may still write, so this does not arise in practice.
func (w *ChanWriter) Write(bytes []byte) (int, error) {
	chanBytes := make([]byte, len(bytes))
	copy(chanBytes, bytes)

	select {
	case w.ch <- chanBytes:
		return len(bytes), nil
	case <-w.done:
		// The session was torn down: unblock a writer parked here rather than
		// wedge the module goroutine. A nil done channel is never selected.
		return 0, io.ErrClosedPipe
	}
}
