// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
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
	"golang.org/x/sys/unix"

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

// setRawInput puts the terminal into raw input mode: disables local echo and
// canonical (line-buffered) mode so each keystroke is available immediately.
// It returns a restore function, or nil if the fd is not a terminal.
func setRawInput(fd int) func() {
	termios, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil
	}

	old := *termios

	termios.Iflag &^= unix.ICRNL
	termios.Lflag &^= unix.ECHO | unix.ICANON
	termios.Cc[unix.VMIN] = 1
	termios.Cc[unix.VTIME] = 0

	if err := unix.IoctlSetTermios(fd, unix.TCSETS, termios); err != nil {
		return nil
	}

	return func() {
		_ = unix.IoctlSetTermios(fd, unix.TCSETS, &old)
	}
}

//nolint:funlen,cyclop,gocognit
func (app *application) runRPC(device, command string, cmdArgs []string) error {
	const numWorkers = 2 // The send and receive worker goroutines

	// Set raw input mode: disable local echo and canonical mode so each
	// keystroke is sent immediately. Interactive modules like serial rely
	// on the remote side to echo input.
	if f, ok := app.stdin.(*os.File); ok {
		if restore := setRawInput(int(f.Fd())); restore != nil {
			defer restore()
		}
	}

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

				content, err := os.ReadFile(path)
				if err != nil {
					errChan <- fmt.Errorf("reading requested file %q: %w", path, err)

					return
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

			default:
				log.Printf("Unexpected message type %T", msg)
			}
		}
	}()

	// Send routine
	go func() {
		defer cancel()

		buf := make([]byte, 256)

		for {
			select {
			case <-runCtx.Done():
				log.Println("Send routine terminating: Run-Context cancelled")

				return
			default:
			}

			n, err := app.stdin.Read(buf)
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
							Stdin: buf[:n],
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
