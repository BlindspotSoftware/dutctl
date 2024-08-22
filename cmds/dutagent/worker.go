package main

import (
	"fmt"
	"log"

	"connectrpc.com/connect"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// toClientWorker sends messages from the module session to the client.
// This function is an infinite loop. It terminates when the session's done channel is closed.
//
//nolint:cyclop
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
		case <-s.done:
			return nil
		}
	}
}

// fromClientWorker reads messages from the client and passes them to the module session.
// This function is an infinite loop. It terminates when the session's done channel is closed.
//
//nolint:cyclop,funlen
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
				// s.file = make(chan []byte, 1) // Buffered channel to be able to close it right after sending the file.
				fileMsg := msg.File
				if fileMsg == nil {
					log.Println("Received nil file message")

					continue // Can this happen?
				}

				path := fileMsg.GetPath()
				file := fileMsg.GetContent()

				if file == nil {
					log.Println("Received nil file content")

					continue // Can this happen?
				}

				log.Printf("Server received file %q from client", path)
				s.file <- file // This will not block, as the channel is buffered.

				log.Println("Passed file to module (buffered in the session)")
				close(s.file)
				log.Println("Closed file channel")
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
