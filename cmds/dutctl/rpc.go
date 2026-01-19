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

// sendFileInChunks sends a file to the agent in chunks to avoid loading the entire file into memory.
// It uses a 1MB chunk size for efficient streaming without excessive memory usage.
func (app *application) sendFileInChunks(stream *connect.BidiStreamForClient[pb.RunRequest, pb.RunResponse], path string) error {
	const chunkSize = 1024 * 1024 // 1MB chunks

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	totalSize := uint64(fileInfo.Size())
	var offset uint64

	buf := make([]byte, chunkSize)

	for {
		n, err := file.Read(buf)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("failed to read file: %w", err)
		}

		if n == 0 {
			break
		}

		isLast := errors.Is(err, io.EOF) || offset+uint64(n) >= totalSize

		chunk := &pb.FileChunk{
			Path:      path,
			Data:      buf[:n],
			Offset:    offset,
			TotalSize: totalSize,
			IsLast:    isLast,
		}

		err = stream.Send(&pb.RunRequest{
			Msg: &pb.RunRequest_FileChunk{
				FileChunk: chunk,
			},
		})
		if err != nil {
			return fmt.Errorf("failed to send chunk at offset %d: %w", offset, err)
		}

		log.Printf("Sent chunk: offset=%d, size=%d, isLast=%v\n", offset, n, isLast)

		offset += uint64(n)

		if isLast {
			break
		}
	}

	return nil
}

// receiveFileChunk handles receiving a file chunk from the agent.
// It creates a new file on the first chunk and appends subsequent chunks.
func (app *application) receiveFileChunk(chunk *pb.FileChunk) error {
	path := chunk.GetPath()
	data := chunk.GetData()
	offset := chunk.GetOffset()
	isLast := chunk.GetIsLast()

	log.Printf("Received chunk: path=%q, offset=%d, size=%d, isLast=%v\n", path, offset, len(data), isLast)

	// Get or create file handle
	file, exists := app.receivingFiles[path]
	if !exists {
		// First chunk - create the file
		perm := 0600
		var err error

		file, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(perm))
		if err != nil {
			return fmt.Errorf("failed to create file %q: %w", path, err)
		}

		app.receivingFiles[path] = file
	}

	// Write chunk data at the specified offset
	_, err := file.WriteAt(data, int64(offset))
	if err != nil {
		file.Close()
		delete(app.receivingFiles, path)

		return fmt.Errorf("failed to write chunk at offset %d: %w", offset, err)
	}

	// If this is the last chunk, close and clean up
	if isLast {
		err = file.Close()
		if err != nil {
			return fmt.Errorf("failed to close file %q: %w", path, err)
		}

		delete(app.receivingFiles, path)
		log.Printf("File transfer complete: %q\n", path)
	}

	return nil
}

//nolint:funlen,cyclop,gocognit
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
			case *pb.RunResponse_FileRequest:
				path := msg.FileRequest.GetPath()
				log.Printf("File request for: %q\n", path)

				err = app.sendFileInChunks(stream, path)
				if err != nil {
					errChan <- fmt.Errorf("sending requested file %q: %w", path, err)

					return
				}

				log.Printf("Sent file: %q\n", path)
			case *pb.RunResponse_File:
				path := msg.File.GetPath()
				content := msg.File.GetContent()

				log.Printf("Received file: %q\n", path)

				if len(content) == 0 {
					log.Println("Received empty file content")
				}

				perm := 0600

				err = os.WriteFile(path, content, fs.FileMode(perm))
				if err != nil {
					errChan <- fmt.Errorf("saving received file %q: %w", path, err)

					return
				}

			case *pb.RunResponse_FileChunk:
				chunk := msg.FileChunk
				err = app.receiveFileChunk(chunk)
				if err != nil {
					errChan <- fmt.Errorf("receiving file chunk: %w", err)

					return
				}

			default:
				log.Printf("Unexpected message type %T", msg)
			}
		}
	}()

	// Send routine
	go func() {
		defer cancel()

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
	}()

	// Wait for completion or error
	select {
	case <-runCtx.Done():
		return nil
	case err := <-errChan:
		return err
	}
}
