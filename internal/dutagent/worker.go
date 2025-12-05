// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dutagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/BlindspotSoftware/dutctl/internal/chanio"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// toClientWorker sends messages from the module session to the client.
// This function is an infinite loop. It terminates when the session's done channel is closed.
//
//nolint:cyclop, funlen
func toClientWorker(ctx context.Context, stream Stream, s *session) error {
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
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_FileRequest{FileRequest: &pb.FileRequest{Path: name}},
			}

			err := stream.Send(res)
			if err != nil {
				return err
			}

			s.currentFile = name
		case file := <-s.fileCh:
			r, err := chanio.NewChanReader(file)
			if err != nil {
				return err
			}

			content, err := io.ReadAll(r)
			if err != nil {
				return err
			}

			log.Printf("Received file from module, sending to client. Name: %q, Size %d", s.currentFile, len(content))

			res := &pb.RunResponse{
				Msg: &pb.RunResponse_File{
					File: &pb.File{
						Path:    s.currentFile,
						Content: content,
					},
				},
			}

			err = stream.Send(res)
			if err != nil {
				return err
			}

			s.currentFile = ""
		}
	}
}

// fromClientWorker reads messages from the client and passes them to the module session.
// This function is an infinite loop. It terminates when the session's done channel is closed.
//
//nolint:cyclop,funlen,gocognit
func fromClientWorker(ctx context.Context, stream Stream, s *session) error {
	type recvResult struct {
		req *pb.RunRequest
		err error
	}

	// Single goroutine performing blocking Receive calls and forwarding results.
	resCh := make(chan recvResult)
	// Receive loop goroutine rationale:
	//
	// We offload blocking stream.Receive calls to this goroutine so the main select
	// can remain responsive to ctx cancellation. The goroutine will keep calling
	// Receive until an error (including io.EOF) occurs, then return.
	//
	// Potential leak concern: If ctx is cancelled while Receive is blocked the
	// goroutine keeps waiting. This is acceptable because, by contract, the RPC
	// stream is closed by the client (EOF) or ends with an error shortly after
	// module completion / broker cancellation; that closure unblocks Receive and
	// the goroutine exits, so it does not leak for the lifetime of the process.
	go func() {
		for {
			req, err := stream.Receive()
			resCh <- recvResult{req: req, err: err}

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
				log.Println("Received nil request without error; ignoring")

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
						log.Println("Received nil stdin message")

						continue
					}

					log.Printf("Server received stdin from client: %q", string(stdin))

					select {
					case <-ctx.Done():
						return nil
					case s.stdinCh <- stdin:
					}

					log.Println("Passed stdin to module")

				default:
					log.Printf("Unexpected Console message %T", consoleMsg)
				}
			case *pb.RunRequest_File:
				fileMsg := msg.File
				if fileMsg == nil {
					log.Println("Received empty file message")

					return fmt.Errorf("bad file transfer: received empty file-message")
				}

				if s.currentFile == "" {
					log.Println("Received file without a request")

					return fmt.Errorf("bad file transfer: received file-message without a former request")
				}

				path := fileMsg.GetPath()
				content := fileMsg.GetContent()

				if content == nil {
					log.Println("Received file message with empty content")

					return fmt.Errorf("bad file transfer: received file-message without content")
				}

				if path != s.currentFile {
					log.Printf("Received unexpected file %q - ignoring!", path)

					return fmt.Errorf("bad file transfer: received file-message %q but requested %q", path, s.currentFile)
				}

				log.Printf("Server received file %q from client", path)

				file := make(chan []byte, 1)
				s.fileCh <- file
				file <- content

				close(file)
				log.Println("Passed file to module (buffered in the session)")

				s.currentFile = ""
			default:
				log.Printf("Unexpected message type %T", msg)
			}
		}
	}
}
