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
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/output"
	"github.com/BlindspotSoftware/dutctl/pkg/lock"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// errInterrupted is returned by runRPC when the run is terminated by a signal
// (Ctrl-C) rather than by the agent or an error. exit() reports it as an
// "interrupted" status with exit code 130, not as a failure.
var errInterrupted = errors.New("interrupted")

func (app *application) listRPC() error {
	// TODO(ctx): unary RPCs run on a bare background context — no signal
	// cancellation and no deadline. A later change should share an app-level
	// context (see runRPC's signal.NotifyContext) and add a timeout. Applies to
	// every unary RPC in this file (List/Lock/Unlock/Commands/Details).
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

//nolint:funlen,cyclop,gocognit // coordinates two streaming worker goroutines; inherently branchy
func (app *application) runRPC(device, command string, cmdArgs []string) error {
	const numWorkers = 2 // The send and receive worker goroutines

	// appCtx is cancelled on SIGINT/SIGTERM so Ctrl-C terminates gracefully
	// (running the normal teardown and flushing the warning summary) instead of
	// killing the process. runCtx is the child the workers cancel on completion.
	appCtx, cancelAppCtx := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelAppCtx()

	runCtx, cancelRunCtx := context.WithCancel(appCtx)
	defer cancelRunCtx()

	errChan := make(chan error, numWorkers)

	// ftManager tracks chunked file transfers for this run: it streams uploads
	// requested by the agent and reassembles downloads chunk by chunk.
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
		defer cancelRunCtx()

		for {
			select {
			case <-runCtx.Done():
				slog.Debug("receive routine terminating", "reason", "run-context cancelled")

				return
			default: // Unblock select, continue with the forwarding logic.
			}

			res, err := stream.Receive()

			switch {
			case errors.Is(err, io.EOF):
				slog.Debug("receive routine terminating", "reason", "stream closed by agent")

				return
			case err != nil && (errors.Is(err, context.Canceled) || connect.CodeOf(err) == connect.CodeCanceled):
				slog.Debug("receive routine terminating", "reason", "context cancelled")

				return
			case err != nil:
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
					slog.Warn("unexpected console stdin from agent", "data", string(consoleData.Stdin))
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
				slog.Warn("unexpected message type", "type", fmt.Sprintf("%T", msg))
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
				slog.Debug("send routine terminating", "reason", "run-context cancelled")

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
		// appCtx.Err() is non-nil only if a signal fired (the deferred
		// cancelAppCtx has not run yet), distinguishing Ctrl-C from a normal
		// stream-closed teardown.
		if appCtx.Err() != nil {
			return errInterrupted
		}

		return nil
	case err := <-errChan:
		return err
	}
}
