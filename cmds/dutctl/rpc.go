// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/output"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

func (app *application) listRPC() error {
	ctx := context.Background()
	req := connect.NewRequest(&pb.ListRequest{})

	res, err := app.rpcClient.List(ctx, req)
	if err != nil {
		return err
	}

	app.formatter.WriteContent(output.Content{
		Type: output.TypeDeviceList,
		Data: res.Msg.GetDevices(),
		Metadata: map[string]string{
			"server": app.serverAddr,
			"msg":    "List Response",
		},
	})

	return nil
}

func (app *application) commandsRPC(device string) error {
	ctx := context.Background()
	req := connect.NewRequest(&pb.CommandsRequest{Device: device})

	res, err := app.rpcClient.Commands(ctx, req)
	if err != nil {
		return err
	}

	app.formatter.WriteContent(output.Content{
		Type: output.TypeCommandList,
		Data: res.Msg.GetCommands(),
		Metadata: map[string]string{
			"server": app.serverAddr,
			"msg":    "Commands Response",
			"device": device,
		},
	})

	return nil
}

func (app *application) detailsRPC(device, command, keyword string) error {
	ctx := context.Background()
	req := connect.NewRequest(&pb.DetailsRequest{
		Device:  device,
		Cmd:     command,
		Keyword: keyword,
	})

	res, err := app.rpcClient.Details(ctx, req)
	if err != nil {
		return err
	}

	app.formatter.WriteContent(output.Content{
		Type: output.TypeCommandDetail,
		Data: res.Msg.GetDetails(),
		Metadata: map[string]string{
			"server":  app.serverAddr,
			"rpc":     "Details Response",
			"device":  device,
			"command": command,
			"keyword": keyword,
		},
	})

	return nil
}

//nolint:funlen,cyclop,gocognit
func (app *application) runRPC(device, command string, cmdArgs []string) error {
	const numWorkers = 2 // The send and receive worker goroutines

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, numWorkers)

	ftManager := newClientFileTransferManager(cmdArgs)

	stream := app.rpcClient.Run(runCtx)
	req := &pb.RunRequest{
		Msg: &pb.RunRequest_Command{
			Command: &pb.Command{
				Device:  device,
				Command: command,
				Args:    cmdArgs,
			},
		},
	}

	err := stream.Send(req)
	if err != nil {
		return err
	}

	metadata := map[string]string{
		"server":  app.serverAddr,
		"msg":     "Run Response",
		"device":  device,
		"command": command,
		"args":    strings.Join(cmdArgs, " "),
	}

	// Receive routine
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Receive routine panic: %v", r)
			}

			cancel()
		}()

		for {
			select {
			case <-runCtx.Done():
				return
			default:
			}

			res, err := stream.Receive()

			if errors.Is(err, io.EOF) {
				return
			} else if err != nil {
				errChan <- fmt.Errorf("receiving RPC message: %w", err)

				return
			}

			//nolint:protogetter
			switch msg := res.Msg.(type) {
			case *pb.RunResponse_Print:
				app.formatter.WriteContent(output.Content{
					Type:     output.TypeModuleOutput,
					Data:     string(msg.Print.GetText()),
					Metadata: metadata,
				})
			case *pb.RunResponse_Console:
				switch consoleData := msg.Console.Data.(type) {
				case *pb.Console_Stdout:
					app.formatter.WriteContent(output.Content{
						Type:     output.TypeModuleOutput,
						Data:     string(consoleData.Stdout),
						Metadata: metadata,
					})
				case *pb.Console_Stderr:
					app.formatter.WriteContent(output.Content{
						Type:     output.TypeModuleOutput,
						Data:     string(consoleData.Stderr),
						IsError:  true,
						Metadata: metadata,
					})
				case *pb.Console_Stdin:
					log.Printf("Unexpected Console Stdin %q", string(consoleData.Stdin))
				}
			case *pb.RunResponse_FileTransferRequest:
				ftReq := msg.FileTransferRequest

				ftErr := ftManager.handleFileTransferRequest(ftReq, stream)
				if ftErr != nil {
					errChan <- ftErr

					return
				}

			case *pb.RunResponse_FileChunk:
				chunk := msg.FileChunk

				chunkErr := ftManager.handleFileChunk(chunk, stream)
				if chunkErr != nil {
					errChan <- chunkErr

					return
				}

			case *pb.RunResponse_FileTransferResponse:
				ftRes := msg.FileTransferResponse
				ftManager.handleFileTransferResponse(ftRes)

			default:
				log.Printf("Unexpected message type %T", msg)
			}
		}
	}()

	// Send routine - only for reading user input (stdin)
	// This is a best-effort routine: EOF on stdin is normal and doesn't cancel the context
	go func() {
		reader := bufio.NewReader(app.stdin)

		for {
			select {
			case <-runCtx.Done():
				log.Println("Send routine terminating: Run-Context cancelled")

				return
			default:
			}

			text, err := reader.ReadString('\n')
			if err != nil {
				if errors.Is(err, io.EOF) {
					// EOF on stdin is normal when there's no interactive input
					log.Println("Send routine: stdin closed (EOF), stopping stdin forwarding")
				} else {
					log.Printf("Send routine: error reading stdin: %v", err)
				}

				return
			}

			sendErr := stream.Send(&pb.RunRequest{
				Msg: &pb.RunRequest_Console{
					Console: &pb.Console{
						Data: &pb.Console_Stdin{
							Stdin: []byte(text),
						},
					},
				},
			})
			if sendErr != nil {
				log.Printf("Send routine: error sending to stream: %v", sendErr)

				return
			}
		}
	}()

	// Wait for completion or error
	select {
	case <-runCtx.Done():
		return nil
	case err := <-errChan:
		return err
	}
}
