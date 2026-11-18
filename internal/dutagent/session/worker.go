// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/module"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// ErrBadFileTransfer marks a malformed file transfer from the client (a protocol
// violation) so the RPC layer can map it to CodeInvalidArgument rather than
// treating it as an internal fault.
var ErrBadFileTransfer = errors.New("bad file transfer")

// response frames the event for the client: a ConsoleOpen with the console's
// id and mode, or a ConsoleClose.
func (e consoleEvent) response() *pb.RunResponse {
	if !e.open {
		return &pb.RunResponse{Msg: &pb.RunResponse_ConsoleClose{ConsoleClose: &pb.ConsoleClose{}}}
	}

	return &pb.RunResponse{
		Msg: &pb.RunResponse_ConsoleOpen{ConsoleOpen: &pb.ConsoleOpen{Id: e.console.id, Mode: wireConsoleMode(e.console.mode)}},
	}
}

// wireConsoleMode is the wire form of mode.
func wireConsoleMode(mode module.ConsoleMode) pb.ConsoleMode {
	if mode == module.ConsoleRaw {
		return pb.ConsoleMode_CONSOLE_MODE_RAW
	}

	return pb.ConsoleMode_CONSOLE_MODE_LINE
}

// ErrStreamClosed reports that the stream could not accept a message because it
// is already gone. It is not a run failure: the workers stop quietly on it,
// since there is no client left to tell. Any other send error is real and
// terminates the worker with that error.
var ErrStreamClosed = errors.New("stream closed")

// sendToClient sends res on the bidirectional stream. Only toClientWorker
// calls it, as the connect BidiStream is not safe for concurrent Send calls.
//
// A Send on an already-closed stream can panic. That is recovered into
// ErrStreamClosed rather than a nil error: reporting success for a message that
// was never sent leaves the caller believing a chunk landed, and leaves this
// worker looping against a dead stream until something else cancels it.
func (s *backend) sendToClient(stream Stream, res *pb.RunResponse) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger().Warn("recovered from panic in stream.Send", "err", r)

			err = fmt.Errorf("%w: send panicked: %v", ErrStreamClosed, r)
		}
	}()

	return stream.Send(res)
}

// sendOrStop delivers res and classifies a failure for a worker loop: a closed
// stream stops the worker without failing the run (there is no client left to
// report to), any other error is terminal and surfaces on the error channel.
func (s *backend) sendOrStop(stream Stream, res *pb.RunResponse) (bool, error) {
	sendErr := s.sendToClient(stream, res)

	switch {
	case sendErr == nil:
		return false, nil
	case errors.Is(sendErr, ErrStreamClosed):
		return true, nil
	default:
		return true, sendErr
	}
}

// toClientWorker sends module output, console traffic and the messages of file
// transfers to the client. It loops until ctx is cancelled (returning nil) or a
// stream send fails (returning that error).
//
// It sends everything, so the order it receives in is the order the client
// sees: a
// console's open event, which OpenConsole hands over before it returns the
// console to the module, precedes the console's first output, and the close
// event, handed over once the console's channels are ended, follows its last.
// It reads output only from the open console; a console that ended has no
// reader any more, and a writer still blocked on it fails.
//
//nolint:cyclop // one select over every source of output; inherently branchy
func toClientWorker(ctx context.Context, stream Stream, s *backend) error {
	l := log.FromContext(ctx)

	// current is the console whose output the worker forwards: the one the
	// last event opened, none after a close. The events arrive in order, a
	// close before the next open, so a close is always the current one's.
	var current *console

	for {
		select {
		case <-ctx.Done():
			return nil
		case str := <-s.printCh:
			// No shutdown check: Print refuses new output once shutdown starts, so
			// anything already on the channel was accepted and must still be sent.
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Print{Print: &pb.Print{Text: []byte(str)}},
			}

			stop, err := s.sendOrStop(stream, res)
			if stop {
				if err != nil {
					l.Warn("error sending print", "err", err)
				}

				return err
			}
		case event := <-s.consoleCh:
			current = nil
			if event.open {
				current = event.console
			}

			stop, err := s.sendOrStop(stream, event.response())
			if stop {
				return err
			}
		case bytes := <-current.stdout():
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_ConsoleOutput{ConsoleOutput: &pb.ConsoleOutput{Data: &pb.ConsoleOutput_Stdout{Stdout: bytes}}},
			}

			stop, err := s.sendOrStop(stream, res)
			if stop {
				return err
			}
		case bytes := <-current.stderr():
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_ConsoleOutput{ConsoleOutput: &pb.ConsoleOutput{Data: &pb.ConsoleOutput_Stderr{Stderr: bytes}}},
			}

			stop, err := s.sendOrStop(stream, res)
			if stop {
				return err
			}
		case res := <-s.fileOut:
			// As for Print, what the module handed over is sent.
			stop, err := s.sendOrStop(stream, res)
			if stop {
				return err
			}
		}
	}
}

// fromClientWorker reads messages from the client and routes them: console
// input and control to the open console, and file-transfer messages (chunks
// and responses) to the transfer they belong to. It loops until ctx is
// cancelled or the client closes the stream with io.EOF (both returning nil),
// or a stream error occurs (returning that error).
//
//nolint:cyclop,funlen,gocognit // one loop over every message the client sends, beside a receiving goroutine
func fromClientWorker(ctx context.Context, stream Stream, s *backend) error {
	l := log.FromContext(ctx)

	type recvResult struct {
		req *pb.RunRequest
		err error
	}

	// Single goroutine performing blocking Receive calls and forwarding results.
	resCh := make(chan recvResult)
	// Receive loop goroutine rationale:
	//
	// We offload blocking stream.Receive calls to this goroutine so the main select
	// can remain responsive to ctx cancellation. The goroutine keeps calling
	// Receive until an error (including io.EOF) occurs, then returns.
	//
	// Two blocking points, both bounded:
	//   - stream.Receive is transport I/O that ctx cannot interrupt; it unblocks
	//     when the client closes the stream (EOF) or it errors, which happens
	//     shortly after module completion / broker cancellation tears the RPC
	//     down. This is an accepted bounded wait.
	//   - the resCh send is guarded by ctx.Done. Once the main loop returns it no
	//     longer receives from resCh, so an unguarded send here would block
	//     forever on a receiverless channel — leaking this goroutine for the
	//     process lifetime. Selecting on ctx.Done lets it exit instead, so the
	//     goroutine always terminates once Receive returns.
	go func() {
		for {
			req, err := stream.Receive()

			select {
			case resCh <- recvResult{req: req, err: err}:
			case <-ctx.Done():
				return
			}

			if err != nil { // stop receiving after any error (including EOF)
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			// Cancellation path: opportunistically drain one pending receive.
			select {
			case r := <-resCh:
				if r.err != nil && !errors.Is(r.err, io.EOF) {
					return r.err
				}

				return nil
			default:
				return nil
			}
		case r := <-resCh:
			if r.err != nil {
				if errors.Is(r.err, io.EOF) {
					return nil
				}

				return r.err
			}

			if r.req == nil { // Defensive: shouldn't happen unless stream.Receive misbehaves
				l.Warn("ignoring nil request without error")

				continue
			}

			reqMsg := r.req.GetMsg()
			switch msg := reqMsg.(type) {
			case *pb.RunRequest_ConsoleInput:
				if !deliverConsoleInput(ctx, s, msg.ConsoleInput) {
					return nil
				}
			case *pb.RunRequest_ConsoleControl:
				handleConsoleControl(ctx, s, msg.ConsoleControl)
			case *pb.RunRequest_FileChunk:
				// A broken chunk is a protocol violation the client cannot
				// recover from, so it ends the run, and the RPC's error tells
				// the client.
				err := s.deliverUploadChunk(ctx, msg.FileChunk)
				if err != nil {
					return err
				}
			case *pb.RunRequest_FileTransferResponse:
				err := s.endByClient(msg.FileTransferResponse)
				if err != nil {
					return err
				}
			case *pb.RunRequest_Command:
				// Command starts a run and is handled by the RPC entrypoint, not
				// here; it should not arrive during an active session.

			default:
				l.Warn("unexpected message type", "type", fmt.Sprintf("%T", msg))
			}
		}
	}
}

// deliverConsoleInput hands the user's input to the open console it is marked
// for, and discards it when no console is open or it is marked for another:
// input is never queued for a later console, and the worker never parks on it,
// so a file transfer behind it stays live. It reports false once ctx is done.
func deliverConsoleInput(ctx context.Context, s *backend, input *pb.ConsoleInput) bool {
	l := log.FromContext(ctx)

	target := s.consoleFor(input.GetId())
	if target == nil {
		l.Debug("dropping console input: no console open for it", "console", input.GetId(), "bytes", len(input.GetData()))

		return true
	}

	data := input.GetData()
	if len(data) == 0 {
		return true
	}

	l.Debug("received stdin from client", "bytes", len(data))

	// An input that ended takes precedence over a reader still parked on the
	// channel: checked first, since a select with both ready picks at random.
	select {
	case <-target.eof:
		l.Debug("dropping console input: the console's input ended", "bytes", len(data))

		return true
	default:
	}

	select {
	case <-ctx.Done():
		return false
	case <-target.eof:
		l.Debug("dropping console input: the console's input ended", "bytes", len(data))
	case target.stdinCh <- data:
	}

	return true
}

// handleConsoleControl applies a console event from the client to the open
// console it is marked for; an event for any other console is dropped.
func handleConsoleControl(ctx context.Context, s *backend, ctl *pb.ConsoleControl) {
	l := log.FromContext(ctx)

	target := s.consoleFor(ctl.GetId())
	if target == nil {
		l.Debug("dropping console control: no console open for it", "console", ctl.GetId())

		return
	}

	switch ctl.GetControl().(type) {
	case *pb.ConsoleControl_Eof:
		l.Debug("console input ended by the client")
		target.endInput()
	default:
		l.Warn("unexpected console control", "type", fmt.Sprintf("%T", ctl.GetControl()))
	}
}
