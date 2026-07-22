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

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// ErrBadFileTransfer marks a malformed file transfer from the client (a protocol
// violation) so the RPC layer can map it to CodeInvalidArgument rather than
// treating it as an internal fault.
var ErrBadFileTransfer = errors.New("bad file transfer")

// toClientWorker sends messages from the module session to the client.
// It loops until ctx is cancelled (returning nil) or a stream send fails
// (returning that error).
//
//nolint:cyclop, funlen
func toClientWorker(ctx context.Context, stream Stream, s *backend) error {
	l := log.FromContext(ctx)

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
		case bytes := <-s.stdoutCh:
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Console{Console: &pb.Console{Data: &pb.Console_Stdout{Stdout: bytes}}},
			}

			err := stream.Send(res)
			if err != nil {
				return err
			}
		case bytes := <-s.stderrCh:
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Console{Console: &pb.Console{Data: &pb.Console_Stderr{Stderr: bytes}}},
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
			case *pb.RunRequest_Console:
				msgConsoleData := msg.Console.GetData()
				switch consoleMsg := msgConsoleData.(type) {
				case *pb.Console_Stdin:
					stdin := consoleMsg.Stdin
					if stdin == nil {
						l.Warn("ignoring nil stdin message")

						continue
					}

					l.Debug("received stdin from client", "bytes", len(stdin))

					select {
					case <-ctx.Done():
						return nil
					case s.stdinCh <- stdin:
					}

				default:
					l.Warn("unexpected console message", "type", fmt.Sprintf("%T", consoleMsg))
				}
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
				case s.fileCh <- file:
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
