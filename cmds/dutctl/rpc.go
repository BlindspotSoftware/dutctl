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
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/output"
	"github.com/BlindspotSoftware/dutctl/pkg/lock"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

func (app *application) listRPC() error {
	ctx := context.Background()
	req := connect.NewRequest(&pb.ListRequest{})

	res, err := app.rpcClient.List(ctx, req)
	if err != nil {
		return err
	}

	devices := make([]output.DeviceEntry, 0, len(res.Msg.GetDevices()))

	for _, info := range res.Msg.GetDevices() {
		entry := output.DeviceEntry{Name: info.GetName()}

		if lock := info.GetLock(); lock != nil {
			entry.Locked = true
			entry.Owner = lock.GetOwner()
			entry.ExpiresAt = lock.GetExpiresAt()
		}

		devices = append(devices, entry)
	}

	app.formatter.WriteContent(output.Content{
		Type: output.TypeDeviceList,
		Data: devices,
		Metadata: map[string]string{
			"server": app.serverAddr,
			"msg":    "List Response",
		},
	})

	return nil
}

// defaultLockDuration is used when the user runs "lock" without a duration.
const defaultLockDuration = 30 * time.Minute

// parseLockDuration resolves the lock duration from the lock command's
// arguments. An empty argument list yields defaultLockDuration. The duration
// must be positive.
func parseLockDuration(cmdArgs []string) (time.Duration, error) {
	if len(cmdArgs) == 0 || cmdArgs[0] == "" {
		return defaultLockDuration, nil
	}

	parsed, err := time.ParseDuration(cmdArgs[0])
	if err != nil {
		return 0, fmt.Errorf("invalid lock duration %q: %w", cmdArgs[0], err)
	}

	if parsed <= 0 {
		return 0, fmt.Errorf("lock duration must be positive, got %q", cmdArgs[0])
	}

	return parsed, nil
}

func (app *application) lockRPC(device string, cmdArgs []string) error {
	duration, err := parseLockDuration(cmdArgs)
	if err != nil {
		return err
	}

	ctx := context.Background()
	req := connect.NewRequest(&pb.LockRequest{
		Device:          device,
		DurationSeconds: int64(duration.Seconds()),
	})
	req.Header().Set(lock.UserHeader, app.user)

	res, err := app.rpcClient.Lock(ctx, req)
	if err != nil {
		return err
	}

	app.formatter.WriteContent(output.Content{
		Type: output.TypeLockResult,
		Data: output.DeviceEntry{
			Name:      res.Msg.GetDevice(),
			Locked:    true,
			Owner:     res.Msg.GetOwner(),
			ExpiresAt: res.Msg.GetExpiresAt(),
		},
		Metadata: map[string]string{
			"server": app.serverAddr,
			"msg":    "Lock Response",
		},
	})

	return nil
}

func (app *application) unlockRPC(device string) error {
	ctx := context.Background()
	req := connect.NewRequest(&pb.UnlockRequest{Device: device, Force: app.force})
	req.Header().Set(lock.UserHeader, app.user)

	_, err := app.rpcClient.Unlock(ctx, req)
	if err != nil {
		return err
	}

	app.formatter.WriteContent(output.Content{
		Type: output.TypeLockResult,
		Data: output.DeviceEntry{Name: device},
		Metadata: map[string]string{
			"server": app.serverAddr,
			"msg":    "Unlock Response",
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
	stream.RequestHeader().Set(lock.UserHeader, app.user)

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

	// Send routine — reads lines from stdin and forwards them to the server.
	//
	// Unlike the receive routine this goroutine intentionally does NOT defer
	// cancel(). When stdin reaches EOF (e.g. /dev/null in non-interactive
	// runs) this goroutine returns immediately. If it cancelled the context
	// on exit, the receive routine would be torn down before it could read
	// and print the server's response.
	//
	// Only the receive routine drives context cancellation so that all
	// server output is processed before the RPC terminates.
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
