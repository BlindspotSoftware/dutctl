// dutagent is the server of the DUT Contol system.
// The service ist designed to run on a single board computer,
// which can handle the wiring to the devices under test (DUTs).
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"

	"connectrpc.com/connect"
	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type dutagent struct{}

func (a *dutagent) List(
	_ context.Context,
	_ *connect.Request[pb.ListRequest],
) (*connect.Response[pb.ListResponse], error) {
	log.Println("Server received List request")

	res := connect.NewResponse(&pb.ListResponse{
		Devices: testlist.names(),
	})

	return res, nil
}

func (a *dutagent) Commands(
	_ context.Context,
	req *connect.Request[pb.CommandsRequest],
) (*connect.Response[pb.CommandsResponse], error) {
	log.Println("Server received Commands request")

	device := req.Msg.GetDevice()

	res := connect.NewResponse(&pb.CommandsResponse{
		Commands: testlist.cmds(device),
	})

	return res, nil
}

func (a *dutagent) Run(
	ctx context.Context,
	stream *connect.BidiStream[pb.RunRequest, pb.RunResponse],
) error {
	log.Println("Server received Run request")

	req, err := stream.Receive()
	if err != nil {
		return connect.NewError(connect.CodeAborted, err)
	}

	cmdMsg := req.GetCommand()
	if cmdMsg == nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("first run request must contain a command"))
	}

	device, ok := testlist[cmdMsg.GetDevice()]
	if !ok {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("device %q does not exist", cmdMsg.GetDevice()))
	}

	cmd, ok := device.Cmds[cmdMsg.GetCmd()]
	if !ok {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("device %q does not have command %q", cmdMsg.GetDevice(), cmdMsg.GetCmd()))
	}

	if len(cmd.Modules) == 0 {
		return connect.NewError(connect.CodeInternal, errors.New("no modules set for command"))
	}

	if len(cmd.Modules) > 1 {
		return connect.NewError(connect.CodeUnimplemented, errors.New("multiple modules per command not supported yet"))
	}

	// First idea: Pass everything important to the module via context
	// also maybe channels for communication back to the stream
	// BUT: Actually, the communication channels should passed explicitly for better understanding.
	// The context should only be used for canceling and passing information that is not changed during the execution.
	// E.g. the device name, the command name, etc.

	sesh := session{
		print:   make(chan string),
		stdin:   make(chan []byte),
		stdout:  make(chan []byte),
		stderr:  make(chan []byte),
		fileReq: make(chan string),
		file:    make(chan []byte, 1),
		done:    make(chan struct{}),
	}

	// Run the module in a goroutine.
	// The termiantion of the module execution is signaled by closing the done channel.
	go func() {
		sesh.err = cmd.Modules[0].Run(ctx, &sesh, cmdMsg.GetArgs()...)
		log.Println("Module finished and returned error: ", sesh.err)
		close(sesh.done)
	}()

	var wg sync.WaitGroup

	// Start a worker for sending messages that are colleced by the session form the module to the client.
	// Use a WaitGroup for synchronization as there might be messages left to send to the client after the module finished.
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("Starting send-to-client worker")
		toClientWorker(stream, sesh)
		log.Println("The send-to-client worker returned")
	}()

	// Start a worker for receiving messages from the client and pass them to the module.
	// Do not use a WaitGroup here, as the worker blocks on on receive calls to the client.
	// In case of a non-interactive module (client does not send further messages), the worker will block forever.
	// and waiting for it will never return.
	// However, if the stream is closed, the receive calls to the client unblock and he worker will return.
	go func() {
		log.Println("Starting receive-from-client worker")
		fromClientWorker(stream, sesh)
		log.Println("The receive-from-client worker returned")
	}()

	log.Println("Waiting for session to finish")

	wg.Wait()

	log.Println("Session finished")

	return sesh.err
}

// toClientWorker sends messages from the session to the client.
// This function is an infinite loop. It terminates when the session is done.
func toClientWorker(stream *connect.BidiStream[pb.RunRequest, pb.RunResponse], s session) error {
	for {
		select {
		case str := <-s.print:
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Print{&pb.Print{Text: []byte(str)}},
			}

			err := stream.Send(res)
			if err != nil {
				return connect.NewError(connect.CodeInternal, err)
			}
		case bytes := <-s.stdout:
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Console{&pb.Console{Data: &pb.Console_Stdout{bytes}}},
			}

			err := stream.Send(res)
			if err != nil {
				return connect.NewError(connect.CodeInternal, err)
			}
		case bytes := <-s.stderr:
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Console{&pb.Console{Data: &pb.Console_Stderr{bytes}}},
			}

			err := stream.Send(res)
			if err != nil {
				return connect.NewError(connect.CodeInternal, err)
			}
		case name := <-s.fileReq:
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_FileRequest{&pb.FileRequest{Path: name}},
			}

			err := stream.Send(res)
			if err != nil {
				return connect.NewError(connect.CodeInternal, err)
			}
		case <-s.done:
			return nil
		}
	}
}

// fromClientWorker reads messages from the client and passes them to the session.
// This function is an infinite loop. It terminates when the the session is done.
func fromClientWorker(stream *connect.BidiStream[pb.RunRequest, pb.RunResponse], s session) error {
	for {
		select {
		case <-s.done:
			return nil
		default:
			req, err := stream.Receive()
			if err != nil {
				return connect.NewError(connect.CodeAborted, err)
			}

			switch msg := req.Msg.(type) {
			case *pb.RunRequest_Console:
				switch consoleMsg := msg.Console.Data.(type) {
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
				//s.file = make(chan []byte, 1) // Buffered channel to be able to close it right after sending the file.
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

func main() {
	agent := &dutagent{}
	mux := http.NewServeMux()
	path, handler := dutctlv1connect.NewDeviceServiceHandler(agent)
	mux.Handle(path, handler)
	err := http.ListenAndServe(
		"localhost:8080",
		// Use h2c so we can serve HTTP/2 without TLS.
		h2c.NewHandler(mux, &http2.Server{}),
	)

	log.Fatalln(err)
}
