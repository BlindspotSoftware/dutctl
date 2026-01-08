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

type recvResult struct {
	req *pb.RunRequest
	err error
}

// toClientWorker sends messages from the module session to the client.
// This function is an infinite loop. It terminates when the session's done channel is closed.
//
//nolint:cyclop
func toClientWorker(ctx context.Context, stream Stream, s *session) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case str := <-s.printCh:
			err := sendPrint(stream, str)
			if err != nil {
				return err
			}
		case bytes := <-s.stdoutCh:
			err := sendStdout(stream, bytes)
			if err != nil {
				return err
			}
		case bytes := <-s.stderrCh:
			err := sendStderr(stream, bytes)
			if err != nil {
				return err
			}
		case name := <-s.fileReqCh:
			err := sendFileRequest(stream, name)
			if err != nil {
				return err
			}

			s.currentFile = name
		case file := <-s.fileCh:
			err := sendFile(stream, file, s.currentFile)
			if err != nil {
				return err
			}

			s.currentFile = ""
		}
	}
}

func sendPrint(stream Stream, str string) error {
	res := &pb.RunResponse{
		Msg: &pb.RunResponse_Print{Print: &pb.Print{Text: []byte(str)}},
	}

	return stream.Send(res)
}

func sendStdout(stream Stream, bytes []byte) error {
	res := &pb.RunResponse{
		Msg: &pb.RunResponse_Console{Console: &pb.Console{Data: &pb.Console_Stdout{Stdout: bytes}}},
	}

	return stream.Send(res)
}

func sendStderr(stream Stream, bytes []byte) error {
	res := &pb.RunResponse{
		Msg: &pb.RunResponse_Console{Console: &pb.Console{Data: &pb.Console_Stderr{Stderr: bytes}}},
	}

	return stream.Send(res)
}

func sendFileRequest(stream Stream, name string) error {
	res := &pb.RunResponse{
		Msg: &pb.RunResponse_FileRequest{FileRequest: &pb.FileRequest{Path: name}},
	}

	return stream.Send(res)
}

func sendFile(stream Stream, file chan []byte, currentFile string) error {
	r, err := chanio.NewChanReader(file)
	if err != nil {
		return err
	}

	content, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	log.Printf("Received file from module, sending to client. Name: %q, Size %d", currentFile, len(content))

	res := &pb.RunResponse{
		Msg: &pb.RunResponse_File{
			File: &pb.File{
				Path:    currentFile,
				Content: content,
			},
		},
	}

	return stream.Send(res)
}

// fromClientWorker reads messages from the client and passes them to the module session.
// This function is an infinite loop. It terminates when the session's done channel is closed.
func fromClientWorker(ctx context.Context, stream Stream, s *session) error {
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
		defer close(resCh)

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
			return handleCancellation(resCh)
		case r, ok := <-resCh:
			if !ok {
				// Channel closed, both send and receive goroutines done
				return nil
			}

			err := handleRecvResult(ctx, r, s)
			if err != nil {
				return err
			}
		}
	}
}

func handleCancellation(resCh chan recvResult) error {
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
}

func handleRecvResult(ctx context.Context, r recvResult, s *session) error {
	if r.err != nil {
		if errors.Is(r.err, io.EOF) {
			return nil
		}

		return r.err
	}

	if r.req == nil { // Defensive: shouldn't happen unless stream.Receive misbehaves
		log.Println("Received nil request without error; ignoring")

		return nil
	}

	reqMsg := r.req.GetMsg()

	switch msg := reqMsg.(type) {
	case *pb.RunRequest_Console:
		if !handleConsoleMessage(ctx, msg, s) {
			return nil
		}
	case *pb.RunRequest_File:
		err := handleFileMessage(msg, s)
		if err != nil {
			return err
		}
	default:
		log.Printf("Unexpected message type %T", msg)
	}

	return nil
}

func handleConsoleMessage(ctx context.Context, msg *pb.RunRequest_Console, s *session) bool {
	msgConsoleData := msg.Console.GetData()

	switch consoleMsg := msgConsoleData.(type) {
	case *pb.Console_Stdin:
		stdin := consoleMsg.Stdin
		if stdin == nil {
			log.Println("Received nil stdin message")

			return false
		}

		log.Printf("Server received stdin from client: %q", string(stdin))

		select {
		case <-ctx.Done():
			return false
		case s.stdinCh <- stdin:
		}

		log.Println("Passed stdin to module")

	default:
		log.Printf("Unexpected Console message %T", consoleMsg)
	}

	return true
}

func handleFileMessage(msg *pb.RunRequest_File, s *session) error {
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

	return nil
}
