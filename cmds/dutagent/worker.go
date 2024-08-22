package main

import (
	"fmt"
	"io"
	"log"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/chanio"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// toClientWorker sends messages from the module session to the client.
// This function is an infinite loop. It terminates when the session's done channel is closed.
//
//nolint:cyclop, funlen
func toClientWorker(stream *connect.BidiStream[pb.RunRequest, pb.RunResponse], s *session) error {
	for {
		select {
		case str := <-s.print:
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Print{Print: &pb.Print{Text: []byte(str)}},
			}

			err := stream.Send(res)
			if err != nil {
				return connect.NewError(connect.CodeInternal, fmt.Errorf("to-client-worker: %v", err))
			}
		case bytes := <-s.stdout:
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Console{Console: &pb.Console{Data: &pb.Console_Stdout{Stdout: bytes}}},
			}

			err := stream.Send(res)
			if err != nil {
				return connect.NewError(connect.CodeInternal, fmt.Errorf("to-client-worker: %v", err))
			}
		case bytes := <-s.stderr:
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Console{Console: &pb.Console{Data: &pb.Console_Stderr{Stderr: bytes}}},
			}

			err := stream.Send(res)
			if err != nil {
				return connect.NewError(connect.CodeInternal, fmt.Errorf("to-client-worker: %v", err))
			}
		case name := <-s.fileReq:
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_FileRequest{FileRequest: &pb.FileRequest{Path: name}},
			}

			err := stream.Send(res)
			if err != nil {
				return connect.NewError(connect.CodeInternal, fmt.Errorf("to-client-worker: %v", err))
			}

			s.currentFile = name
		case file := <-s.file:
			r, err := chanio.NewChanReader(file)
			if err != nil {
				return connect.NewError(connect.CodeInternal, fmt.Errorf("to-client-worker: %v", err))
			}

			content, err := io.ReadAll(r)
			if err != nil {
				return connect.NewError(connect.CodeInternal, fmt.Errorf("to-client-worker: %v", err))
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
				return connect.NewError(connect.CodeInternal, fmt.Errorf("to-client-worker: %v", err))
			}

			s.currentFile = ""
		case <-s.done:
			return nil
		}
	}
}

// fromClientWorker reads messages from the client and passes them to the module session.
// This function is an infinite loop. It terminates when the session's done channel is closed.
//
//nolint:cyclop,funlen,gocognit
func fromClientWorker(stream *connect.BidiStream[pb.RunRequest, pb.RunResponse], s *session) error {
	for {
		select {
		case <-s.done:
			return nil
		default:
			req, err := stream.Receive()
			if err != nil {
				return connect.NewError(connect.CodeAborted, fmt.Errorf("from-client-worker: %v", err))
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
					s.stdin <- stdin

					log.Println("Passed stdin to module")
				default:
					log.Printf("Unexpected Console message %T", consoleMsg)
				}
			case *pb.RunRequest_File:
				fileMsg := msg.File
				if fileMsg == nil {
					log.Println("Received empty file message")

					continue // Can this happen?
				}

				if s.currentFile == "" {
					log.Println("Received file without a request - ignoring!")

					continue // Ignore unexpected files
				}

				path := fileMsg.GetPath()
				content := fileMsg.GetContent()

				if content == nil {
					log.Println("Received file message with empty content")

					continue // Can this happen?
				}

				if path != s.currentFile {
					log.Printf("Received unexpected file %q - ignoring!", path)

					continue // Ignore unexpected files
				}

				log.Printf("Server received file %q from client", path)

				file := make(chan []byte, 1)
				s.file <- file
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
