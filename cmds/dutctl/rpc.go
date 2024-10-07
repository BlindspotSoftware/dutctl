// Copyright 2024 Blindspot Software
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
	"sync"

	"connectrpc.com/connect"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

func (app *application) listRPC() error {
	ctx := context.Background()
	req := connect.NewRequest(&pb.ListRequest{})

	res, err := app.rpcClient.List(ctx, req)
	if err != nil {
		return err
	}

	fmt.Fprintln(app.stdout, res.Msg.GetDevices())

	return nil
}

func (app *application) commandsRPC(device string) error {
	ctx := context.Background()
	req := connect.NewRequest(&pb.CommandsRequest{Device: device})

	res, err := app.rpcClient.Commands(ctx, req)
	if err != nil {
		return err
	}

	fmt.Fprintln(app.stdout, res.Msg.GetCommands())

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

	fmt.Fprintln(app.stdout, res.Msg.GetDetails())

	return nil
}

//nolint:funlen,cyclop,gocognit
func (app *application) runRPC(device, command string, cmdArgs []string) error {
	wg := sync.WaitGroup{}

	ctx := context.Background()
	stream := app.rpcClient.Run(ctx)
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

	// Receive routine
	wg.Add(1)

	go func() {
		defer wg.Done()

		for {
			res, err := stream.Receive()
			if errors.Is(err, io.EOF) {
				log.Println("Receive routine terminating: Stream closed by agent")

				return
			} else if err != nil {
				log.Fatalln(err)
			}

			//nolint:protogetter
			switch msg := res.Msg.(type) {
			case *pb.RunResponse_Print:
				fmt.Fprintln(app.stdout, string(msg.Print.GetText()))
			case *pb.RunResponse_Console:
				switch consoleData := msg.Console.Data.(type) {
				case *pb.Console_Stdout:
					fmt.Fprint(app.stdout, string(consoleData.Stdout))
				case *pb.Console_Stderr:
					fmt.Fprint(app.stdout, string(consoleData.Stderr))
				case *pb.Console_Stdin:
					log.Printf("Unexpected Console Stdin %q", string(consoleData.Stdin))
				}
			case *pb.RunResponse_FileRequest:
				path := msg.FileRequest.GetPath()
				log.Printf("File request for: %q\n", path)

				if !isPartOfArgs(cmdArgs, path) {
					log.Fatalf("Invalid file request: Requested file %q was not named in the command's arguments", path)
				}

				content, err := os.ReadFile(path)
				if err != nil {
					log.Fatal(err)
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
					log.Fatal(err)
				}

				log.Printf("Sent file: %q\n", "file.txt")
			case *pb.RunResponse_File:
				path := msg.File.GetPath()
				content := msg.File.GetContent()

				log.Printf("Received file: %q\n", path)

				if !isPartOfArgs(cmdArgs, path) {
					log.Fatalf("Invalid file transmission: Sent file %q was not named in the command's arguments", path)
				}

				if len(content) == 0 {
					log.Println("Received empty file content")
				}

				perm := 0600

				err = os.WriteFile(path, content, fs.FileMode(perm))
				if err != nil {
					log.Fatal(err)
				}

			default:
				log.Printf("Unexpected message type %T", msg)
			}
		}
	}()

	// Send routine
	// No wg.Add(1) as this routine blocks on reading input, so waiting on this routine
	// is a deadlock. It will be killed, when the applications exits.
	//
	// No clue how to signal the send routine to stop, as it will block on the reader.
	// Maybe set the source of the reader to nil to unblock and check some condition / done-channel?
	go func() {
		reader := bufio.NewReader(app.stdin)

		for {
			text, err := reader.ReadString('\n')
			if err != nil {
				log.Fatalln(err)
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
				log.Fatalln(err)
			}
		}
	}()

	wg.Wait()

	return nil
}

func isPartOfArgs(args []string, token string) bool {
	for _, arg := range args {
		if strings.Contains(arg, token) {
			return true
		}
	}

	return false
}
