// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package dutagent

import (
	"context"
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
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			req, err := stream.Receive()
			if err != nil {
				return err
			}

			reqMsg := req.GetMsg()
			switch msg := reqMsg.(type) {
			case *pb.RunRequest_Console:
				msgConsoleData := msg.Console.GetData()
				switch consoleMsg := msgConsoleData.(type) {
				case *pb.Console_Stdin:
					stdin := consoleMsg.Stdin
					if stdin == nil {
						log.Println("Received nil stdin message")

						continue // Can this happen?
					}

					log.Printf("Server received stdin from client: %q", string(stdin))
					s.stdinCh <- stdin

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
				close(file) // indicate EOF.

				log.Println("Passed file to module (buffered in the session)")

				s.currentFile = ""
			default:
				log.Printf("Unexpected message type %T", msg)
			}

			consoleMsg := req.GetConsole()
			if consoleMsg == nil {
				continue // Ignore non-console messages
			}
		}
	}
}
