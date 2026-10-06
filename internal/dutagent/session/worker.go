// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/BlindspotSoftware/dutctl/internal/chanio"
	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/module"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// ErrBadFileTransfer marks a malformed file transfer from the client (a protocol
// violation) so the RPC layer can map it to CodeInvalidArgument rather than
// treating it as an internal fault.
var ErrBadFileTransfer = errors.New("bad file transfer")

// consoleEventResponse frames event for the client and returns the console
// that is open after it, given the one open before it.
func consoleEventResponse(cur *console, event consoleEvent) (*console, *pb.RunResponse) {
	if event.open {
		mode := pb.ConsoleMode_CONSOLE_MODE_LINE
		if event.cons.mode == module.ConsoleRaw {
			mode = pb.ConsoleMode_CONSOLE_MODE_RAW
		}

		return event.cons, &pb.RunResponse{
			Msg: &pb.RunResponse_ConsoleOpen{ConsoleOpen: &pb.ConsoleOpen{Id: event.cons.id, Mode: mode}},
		}
	}

	if cur == event.cons {
		cur = nil
	}

	return cur, &pb.RunResponse{Msg: &pb.RunResponse_ConsoleClose{ConsoleClose: &pb.ConsoleClose{}}}
}

// toClientWorker sends messages from the module session to the client.
// It loops until ctx is cancelled (returning nil) or a stream send fails
// (returning that error).
//
// It is the one sender on the stream, so the order it receives in is the order
// the client sees: a console's open event, which OpenConsole hands over before
// it returns the console to the module, precedes the console's first output,
// and the close event, handed over once the console's channels are ended,
// follows its last. It reads output only from the open console; a console that
// ended has no reader any more, and a writer still blocked on it fails.
//
//nolint:cyclop, funlen
func toClientWorker(ctx context.Context, stream Stream, s *backend) error {
	l := log.FromContext(ctx)

	var cur *console

	for {
		select {
		case <-ctx.Done():
			return nil
		case str := <-s.printCh:
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Print{Print: &pb.Print{Text: []byte(str)}},
			}

			err := stream.Send(res)
			if err != nil {
				return err
			}
		case event := <-s.consoleCh:
			var res *pb.RunResponse

			cur, res = consoleEventResponse(cur, event)

			err := stream.Send(res)
			if err != nil {
				return err
			}
		case bytes := <-cur.stdout():
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_ConsoleOutput{ConsoleOutput: &pb.ConsoleOutput{Data: &pb.ConsoleOutput_Stdout{Stdout: bytes}}},
			}

			err := stream.Send(res)
			if err != nil {
				return err
			}
		case bytes := <-cur.stderr():
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_ConsoleOutput{ConsoleOutput: &pb.ConsoleOutput{Data: &pb.ConsoleOutput_Stderr{Stderr: bytes}}},
			}

			err := stream.Send(res)
			if err != nil {
				return err
			}
		case name := <-s.fileReqCh:
			// Record the in-flight file before sending the request: the client's
			// response is driven by this Send, so setting currentFile afterwards
			// could race a fast response that fromClientWorker validates against
			// currentFile (see the currentFile guards there).
			s.setCurrentFile(name)

			res := &pb.RunResponse{
				Msg: &pb.RunResponse_FileRequest{FileRequest: &pb.FileRequest{Path: name}},
			}

			err := stream.Send(res)
			if err != nil {
				return err
			}
		case file := <-s.fileCh:
			// The channel is fed and closed by SendFile right after the
			// rendezvous, so this read always terminates: pass a nil done.
			r, err := chanio.NewChanReader(file, nil, l)
			if err != nil {
				return err
			}

			content, err := io.ReadAll(r)
			if err != nil {
				return err
			}

			name := s.currentFileName()

			l.Debug("file received from module", "name", name, "bytes", len(content))

			res := &pb.RunResponse{
				Msg: &pb.RunResponse_File{
					File: &pb.File{
						Path:    name,
						Content: content,
					},
				},
			}

			err = stream.Send(res)
			if err != nil {
				return err
			}

			s.setCurrentFile("")
		}
	}
}

// fromClientWorker reads messages from the client and passes them to the module session.
// It loops until ctx is cancelled or the client closes the stream with io.EOF (both
// returning nil), or a stream/protocol error occurs (returning that error).
//
//nolint:cyclop,funlen,gocognit
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
			case *pb.RunRequest_File:
				fileMsg := msg.File
				if fileMsg == nil {
					return fmt.Errorf("%w: received empty file-message", ErrBadFileTransfer)
				}

				want := s.currentFileName()
				if want == "" {
					return fmt.Errorf("%w: received file-message without a former request", ErrBadFileTransfer)
				}

				path := fileMsg.GetPath()
				content := fileMsg.GetContent()

				if content == nil {
					return fmt.Errorf("%w: received file-message without content", ErrBadFileTransfer)
				}

				if path != want {
					return fmt.Errorf("%w: received file-message %q but requested %q", ErrBadFileTransfer, path, want)
				}

				l.Debug("received file from client", "name", path, "bytes", len(content))

				file := make(chan []byte, 1)

				// Hand the file to the module's RequestFile. Unlike the stdin
				// send above, the receiver is the module goroutine, which may
				// already be gone on teardown; guard the send with ctx.Done so an
				// abandoned transfer cannot wedge this worker (and, through
				// wg.Wait, the broker) forever. The buffered content send and
				// close below never block once the rendezvous succeeds.
				select {
				case s.uploadCh <- file:
				case <-ctx.Done():
					return nil
				}

				file <- content

				close(file)

				s.setCurrentFile("")
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

	cons := s.currentConsole()
	if cons == nil || cons.id != input.GetId() {
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
	case <-cons.eof:
		l.Debug("dropping console input: the console's input ended", "bytes", len(data))

		return true
	default:
	}

	select {
	case <-ctx.Done():
		return false
	case <-cons.eof:
		l.Debug("dropping console input: the console's input ended", "bytes", len(data))
	case cons.stdinCh <- data:
	}

	return true
}

// handleConsoleControl applies a console event from the client to the open
// console it is marked for; an event for any other console is dropped.
func handleConsoleControl(ctx context.Context, s *backend, ctl *pb.ConsoleControl) {
	l := log.FromContext(ctx)

	cons := s.currentConsole()
	if cons == nil || cons.id != ctl.GetId() {
		l.Debug("dropping console control: no console open for it", "console", ctl.GetId())

		return
	}

	switch ctl.GetControl().(type) {
	case *pb.ConsoleControl_Eof:
		l.Debug("console input ended by the client")
		cons.endInput()
	default:
		l.Warn("unexpected console control", "type", fmt.Sprintf("%T", ctl.GetControl()))
	}
}
