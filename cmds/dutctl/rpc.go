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
	"io/fs"
	"log"
	"os"
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

func (app *application) runRPC(device, command string, cmdArgs []string) error {
	const numWorkers = 2 // The send and receive worker goroutines

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, numWorkers)

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
		defer cancel()

		app.receiveLoop(runCtx, stream, metadata, errChan)
	}()

	// Send routine
	go func() {
		defer cancel()

		app.sendLoop(runCtx, stream, errChan)
	}()

	// Wait for completion or error
	select {
	case <-runCtx.Done():
		return nil
	case err := <-errChan:
		return err
	}
}

func (app *application) receiveLoop(
	runCtx context.Context,
	stream *connect.BidiStreamForClient[pb.RunRequest, pb.RunResponse],
	metadata map[string]string,
	errChan chan error,
) {
	for {
		select {
		case <-runCtx.Done():
			log.Println("Receive routine terminating: Run-Context cancelled")

			return
		default: // Unblock select, continue with the forwarding logic.
		}

		res, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			log.Println("Receive routine terminating: Stream closed by agent")

			return
		} else if err != nil {
			errChan <- fmt.Errorf("receiving RPC message: %w", err)

			return
		}

		err = app.handleRunResponse(res, metadata, stream)
		if err != nil {
			errChan <- err

			return
		}
	}
}

func (app *application) handleRunResponse(
	res *pb.RunResponse,
	metadata map[string]string,
	stream *connect.BidiStreamForClient[pb.RunRequest, pb.RunResponse],
) error {
	switch res.GetMsg().(type) {
	case *pb.RunResponse_Print:
		app.formatter.WriteContent(output.Content{
			Type:     output.TypeModuleOutput,
			Data:     string(res.GetPrint().GetText()),
			Metadata: metadata,
		})
	case *pb.RunResponse_Console:
		app.handleConsole(res, metadata)
	case *pb.RunResponse_FileRequest:
		err := app.handleFileRequest(res, stream)
		if err != nil {
			return err
		}
	case *pb.RunResponse_File:
		err := app.handleFileReceive(res)
		if err != nil {
			return err
		}
	default:
		log.Printf("Unexpected message type %T", res.GetMsg())
	}

	return nil
}

func (app *application) handleConsole(res *pb.RunResponse, metadata map[string]string) {
	switch consoleData := res.GetConsole().GetData().(type) {
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
}

func (app *application) handleFileRequest(
	res *pb.RunResponse,
	stream *connect.BidiStreamForClient[pb.RunRequest, pb.RunResponse],
) error {
	path := res.GetFileRequest().GetPath()
	log.Printf("File request for: %q\n", path)

	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading requested file %q: %w", path, err)
	}

	err = stream.Send(&pb.RunRequest{
		Msg: &pb.RunRequest_File{
			File: &pb.File{
				Path:    path,
				Content: content,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("sending requested file %q: %w", path, err)
	}

	log.Printf("Sent file: %q\n", path)

	return nil
}

func (app *application) handleFileReceive(res *pb.RunResponse) error {
	path := res.GetFile().GetPath()
	content := res.GetFile().GetContent()

	log.Printf("Received file: %q\n", path)

	if len(content) == 0 {
		log.Println("Received empty file content")
	}

	perm := 0600

	err := os.WriteFile(path, content, fs.FileMode(perm))
	if err != nil {
		return fmt.Errorf("saving received file %q: %w", path, err)
	}

	return nil
}

func (app *application) sendLoop(
	runCtx context.Context,
	stream *connect.BidiStreamForClient[pb.RunRequest, pb.RunResponse],
	errChan chan error,
) {
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
			if !errors.Is(err, io.EOF) {
				errChan <- fmt.Errorf("reading stdin: %w", err)
			}

			return
		}

		err = stream.Send(&pb.RunRequest{
			Msg: &pb.RunRequest_Console{
				Console: &pb.Console{
					Data: &pb.Console_Stdin{
						Stdin: []byte(text),
					},
				},
			},
		})
		if err != nil {
			errChan <- fmt.Errorf("sending RPC message: %w", err)

			return
		}
	}
}
