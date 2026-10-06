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
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/output"
	"github.com/BlindspotSoftware/dutctl/pkg/headers"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// errInterrupted is returned by runRPC when the run is terminated by a signal
// (Ctrl-C) rather than by the agent or an error. exit() reports it as an
// "interrupted" status with exit code 130, not as a failure.
var errInterrupted = errors.New("interrupted")

// unaryTimeout bounds each non-streaming RPC. List/Lock/Unlock/Commands/Details
// are quick request/response round-trips, so a modest per-call deadline catches
// an unresponsive agent without cutting legitimate work. Connect encodes it as a
// grpc-timeout header, so the agent handler inherits the same deadline. The
// streaming Run deliberately has no overall deadline (see runRPC).
const unaryTimeout = 30 * time.Second

func (app *application) listRPC(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, unaryTimeout)
	defer cancel()

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
			output.MetaServer: app.serverAddr,
			output.MetaMsg:    "List Response",
		},
	})

	return nil
}

// longLockWarnThreshold is the duration above which the client warns that a
// lock is unusually long. It nudges cooperative use and never blocks the
// request; the agent enforces no maximum.
const longLockWarnThreshold = 8 * time.Hour

// parseLockDuration resolves the lock duration from the lock command's
// arguments. An empty argument list yields 0, which tells the agent to apply
// its own default duration. An explicit duration must be positive. On failure
// it returns an error whose message is user-facing display text (an invalid or
// non-positive duration), not a sentinel to match.
func parseLockDuration(cmdArgs []string) (time.Duration, error) {
	if len(cmdArgs) == 0 || cmdArgs[0] == "" {
		return 0, nil
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

func (app *application) lockRPC(ctx context.Context, device string, cmdArgs []string) error {
	duration, err := parseLockDuration(cmdArgs)
	if err != nil {
		return err
	}

	if duration > longLockWarnThreshold {
		slog.Warn("requested a long lock duration; release the device when you are done", "duration", duration)
	}

	ctx, cancel := context.WithTimeout(ctx, unaryTimeout)
	defer cancel()

	req := connect.NewRequest(&pb.LockRequest{
		Device:          device,
		DurationSeconds: int64(duration.Seconds()),
	})
	req.Header().Set(headers.User, app.user)

	res, err := app.rpcClient.Lock(ctx, req)
	if err != nil {
		return err
	}

	app.formatter.WriteContent(output.Content{
		Type: output.TypeLockResult,
		Data: output.DeviceEntry{
			Name:      res.Msg.GetDevice(),
			Locked:    true,
			Owner:     res.Msg.GetLock().GetOwner(),
			ExpiresAt: res.Msg.GetLock().GetExpiresAt(),
		},
		Metadata: map[string]string{
			output.MetaServer: app.serverAddr,
			output.MetaMsg:    "Lock Response",
		},
	})

	return nil
}

func (app *application) unlockRPC(ctx context.Context, device string, force bool) error {
	ctx, cancel := context.WithTimeout(ctx, unaryTimeout)
	defer cancel()

	req := connect.NewRequest(&pb.UnlockRequest{Device: device, Force: force})
	req.Header().Set(headers.User, app.user)

	_, err := app.rpcClient.Unlock(ctx, req)
	if err != nil {
		return err
	}

	app.formatter.WriteContent(output.Content{
		Type: output.TypeLockResult,
		Data: output.DeviceEntry{Name: device},
		Metadata: map[string]string{
			output.MetaServer: app.serverAddr,
			output.MetaMsg:    "Unlock Response",
		},
	})

	return nil
}

func (app *application) commandsRPC(ctx context.Context, device string) error {
	ctx, cancel := context.WithTimeout(ctx, unaryTimeout)
	defer cancel()

	req := connect.NewRequest(&pb.CommandsRequest{Device: device})

	res, err := app.rpcClient.Commands(ctx, req)
	if err != nil {
		return err
	}

	app.formatter.WriteContent(output.Content{
		Type: output.TypeCommandList,
		Data: res.Msg.GetCommands(),
		Metadata: map[string]string{
			output.MetaServer: app.serverAddr,
			output.MetaMsg:    "Commands Response",
			output.MetaDevice: device,
		},
	})

	return nil
}

func (app *application) detailsRPC(ctx context.Context, device, command, keyword string) error {
	ctx, cancel := context.WithTimeout(ctx, unaryTimeout)
	defer cancel()

	req := connect.NewRequest(&pb.DetailsRequest{
		Device:  device,
		Command: command,
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
			output.MetaServer:  app.serverAddr,
			output.MetaMsg:     "Details Response",
			output.MetaDevice:  device,
			output.MetaCommand: command,
			"keyword":          keyword,
		},
	})

	return nil
}

// quitGrace is how long the client waits, after the user ended a raw console
// with the escape sequence, for the module to end the run on its own: the
// serial bridge does so within a second of its input's end. A module that
// does not is cancelled, so quitting works against a stuck one as well.
const quitGrace = 3 * time.Second

// inputChunk is the size of one read from stdin: on a raw terminal a few keys,
// on a pipe one message's worth.
const inputChunk = 256

// runStream is the client's side of the Run stream.
type runStream = *connect.BidiStreamForClient[pb.RunRequest, pb.RunResponse]

// sender serialises the sends of a run: connect allows one Send at a time on
// a stream, and the receive routine (file replies) and the send routine
// (input) both send.
type sender struct {
	mu     sync.Mutex
	stream runStream
}

func (s *sender) send(req *pb.RunRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.stream.Send(req)
}

func inputReq(id uint32, data []byte) *pb.RunRequest {
	return &pb.RunRequest{Msg: &pb.RunRequest_ConsoleInput{ConsoleInput: &pb.ConsoleInput{Id: id, Data: data}}}
}

func eofReq(id uint32) *pb.RunRequest {
	return &pb.RunRequest{Msg: &pb.RunRequest_ConsoleControl{ConsoleControl: &pb.ConsoleControl{
		Id: id, Control: &pb.ConsoleControl_Eof{Eof: &pb.ConsoleEof{}},
	}}}
}

// runRPC executes command on device, streaming module output, driving the
// console a module opens and forwarding input and file transfers until the
// run ends. It returns nil on normal completion, errInterrupted when a signal
// (Ctrl-C) ended the run, or a wrapped error from a worker goroutine (stream
// send/receive or file I/O). A connect status from the agent surfaces through
// the returned error; exit() renders it.
func (app *application) runRPC(ctx context.Context, device, command string, cmdArgs []string) error {
	const numWorkers = 2 // The send and receive worker goroutines

	// ctx is the shared signal context from dispatch, cancelled on SIGINT,
	// SIGTERM and SIGHUP so a signal terminates gracefully (running the normal
	// teardown, restoring the terminal and flushing the warning summary)
	// instead of killing the process. A stream has no overall deadline. runCtx
	// is the child the workers cancel on completion.
	runCtx, cancelRunCtx := context.WithCancel(ctx)
	defer cancelRunCtx()

	con := app.console
	if con == nil {
		con = newConsole(app.stdin, app.stdout, app.stderr, true)
	}

	// Raw mode ends with the run on every path: a normal end, the escape
	// sequence, a signal, a lost connection.
	defer con.finish()

	errChan := make(chan error, numWorkers)

	stream := app.rpcClient.Run(runCtx)
	stream.RequestHeader().Set(headers.User, app.user)

	out := &sender{stream: stream}

	err := out.send(&pb.RunRequest{
		Msg: &pb.RunRequest_Command{
			Command: &pb.Command{
				Device:  device,
				Command: command,
				Args:    cmdArgs,
			},
		},
	})
	if err != nil {
		return err
	}

	metadata := map[string]string{
		output.MetaServer:  app.serverAddr,
		output.MetaMsg:     "Run Response",
		output.MetaDevice:  device,
		output.MetaCommand: command,
		output.MetaArgs:    strings.Join(cmdArgs, " "),
	}

	go app.receive(runCtx, cancelRunCtx, stream, out, con, metadata, errChan)
	go app.sendInput(runCtx, cancelRunCtx, out, con, errChan)

	// Wait for completion or error
	select {
	case <-runCtx.Done():
		// ctx.Err() is non-nil only if a signal fired (dispatch's deferred stop
		// has not run yet), distinguishing Ctrl-C from a normal stream-closed
		// teardown.
		if ctx.Err() != nil {
			return errInterrupted
		}

		return nil
	case err := <-errChan:
		return err
	}
}

// receive is the receive routine: it renders the agent's responses, drives
// the console with the agent's events and answers file requests, until the
// stream ends. It cancels the run when it returns, so a run never outlives
// its stream.
//
//nolint:funlen,cyclop // one switch over every kind of response; inherently branchy
func (app *application) receive(
	ctx context.Context, cancel context.CancelFunc, stream runStream, out *sender, con *console,
	metadata map[string]string, errChan chan<- error,
) {
	defer cancel()

	for {
		select {
		case <-ctx.Done():
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
		case *pb.RunResponse_ConsoleOutput:
			app.consoleOutput(con, msg.ConsoleOutput, metadata)
		case *pb.RunResponse_ConsoleOpen:
			if con.opened(msg.ConsoleOpen.GetId(), msg.ConsoleOpen.GetMode()) {
				// The user's input ended before this console opened: say so now.
				// A send that fails because the stream is over is not the run's
				// error: the next Receive reports how the agent ended the run.
				err = out.send(eofReq(msg.ConsoleOpen.GetId()))
				if err != nil && !errors.Is(err, io.EOF) {
					errChan <- fmt.Errorf("sending end of input: %w", err)

					return
				}
			}
		case *pb.RunResponse_ConsoleClose:
			con.closed()
		case *pb.RunResponse_FileRequest:
			path := msg.FileRequest.GetPath()
			slog.Debug("file requested by agent", "path", path)

			content, err := os.ReadFile(path)
			if err != nil {
				errChan <- fmt.Errorf("reading requested file %q: %w", path, err)

				return
			}

			err = out.send(&pb.RunRequest{
				Msg: &pb.RunRequest_File{
					File: &pb.File{
						Path:    path,
						Content: content,
					},
				},
			})
			if err != nil && !errors.Is(err, io.EOF) {
				errChan <- fmt.Errorf("sending requested file %q: %w", path, err)

				return
			}

			app.formatter.WriteContent(output.Content{
				Type:     output.TypeFileTransfer,
				Data:     output.FileTransfer{Direction: "sent", Path: path, Bytes: len(content)},
				Metadata: metadata,
			})
		case *pb.RunResponse_File:
			path := msg.File.GetPath()
			content := msg.File.GetContent()

			if len(content) == 0 {
				slog.Warn("received empty file content", "path", path)
			}

			perm := 0600

			err = os.WriteFile(path, content, fs.FileMode(perm))
			if err != nil {
				errChan <- fmt.Errorf("saving received file %q: %w", path, err)

				return
			}

			app.formatter.WriteContent(output.Content{
				Type:     output.TypeFileTransfer,
				Data:     output.FileTransfer{Direction: "received", Path: path, Bytes: len(content)},
				Metadata: metadata,
			})

		default:
			slog.Warn("unexpected message type", "type", fmt.Sprintf("%T", msg))
		}
	}
}

// consoleOutput shows the module's console output: on a raw terminal as it
// is, otherwise through the formatter like Print output. The module's standard
// error goes to the client's.
func (app *application) consoleOutput(con *console, msg *pb.ConsoleOutput, metadata map[string]string) {
	var (
		data  []byte
		isErr bool
	)

	switch d := msg.GetData().(type) {
	case *pb.ConsoleOutput_Stdout:
		data = d.Stdout
	case *pb.ConsoleOutput_Stderr:
		data, isErr = d.Stderr, true
	}

	if con.output(data, isErr) {
		return
	}

	app.formatter.WriteContent(output.Content{
		Type:     output.TypeModuleOutput,
		Data:     string(data),
		IsError:  isErr,
		Metadata: metadata,
	})
}

// sendInput is the send routine: it forwards the user's input to the console
// the agent has open, marked with its id, and nothing before one is open, so
// input never reaches a module that reads none. On a raw terminal it applies
// the escape sequence first. The end of the input is passed on as an event:
// on a pipe it ends the routine, for the input is over for good, and every
// later console is told as it opens; on a terminal, where Ctrl-D ends one
// console's input, the routine waits for the next console.
//
// The console may change while a read blocks, so the console an event or a
// chunk is for is resolved once the read returned, never before.
//
// The routine does not cancel the run when the input ends, so the agent's
// remaining output is still received; the quit sequence ends the run through
// the console, with a grace period before the run is cancelled. stdin's Read
// is not interruptible, so after a run that ended otherwise the routine stays
// parked in it until the process exits; dutctl runs one command per
// invocation, so nothing is lost.
func (app *application) sendInput(
	ctx context.Context, cancel context.CancelFunc, out *sender, con *console, errChan chan<- error,
) {
	buf := make([]byte, inputChunk)

	var (
		esc    escapeFilter
		ended  uint32 // the console whose input ended, on a terminal
		filled uint32 // the console the escape filter's state belongs to
	)

	for {
		if _, ok := con.await(ctx, ended); !ok {
			return
		}

		count, err := app.stdin.Read(buf)
		if count > 0 {
			quit, sendErr := handleInput(ctx, out, con, &esc, &filled, buf[:count])
			if sendErr != nil {
				reportSendError(sendErr, errChan)

				return
			}

			if quit {
				quitConsole(ctx, cancel, out, con)

				return
			}
		}

		if err == nil {
			continue
		}

		if !errors.Is(err, io.EOF) {
			errChan <- fmt.Errorf("reading stdin: %w", err)

			return
		}

		target, sendErr := endInput(out, con)
		if sendErr != nil {
			reportSendError(sendErr, errChan)

			return
		}

		if !con.tty {
			return
		}

		ended = target
	}
}

// reportSendError passes a failed send on as the run's error, unless the
// stream is simply over: connect reports that as io.EOF on Send, and the
// receive routine then learns how the agent ended the run and reports that.
func reportSendError(err error, errChan chan<- error) {
	if errors.Is(err, io.EOF) {
		slog.Debug("send routine terminating", "reason", "stream closed")

		return
	}

	errChan <- err
}

// handleInput forwards keys to the console open now, waiting for one if the
// console closed while the keys were read. A pending escape prefix does not
// carry over into another console: the filter starts afresh for each.
func handleInput(
	ctx context.Context, out *sender, con *console, esc *escapeFilter, filled *uint32, keys []byte,
) (bool, error) {
	target, ok := con.await(ctx, 0)
	if !ok {
		return false, nil
	}

	if target != *filled {
		*esc = escapeFilter{}
		*filled = target
	}

	return forwardInput(out, con, target, esc, keys)
}

// forwardInput sends keys, filtered on a raw terminal, to the console target,
// and reports whether the quit sequence was seen.
func forwardInput(out *sender, con *console, target uint32, esc *escapeFilter, keys []byte) (bool, error) {
	chunk, quit := con.filterInput(esc, keys)
	if len(chunk) == 0 {
		return quit, nil
	}

	err := out.send(inputReq(target, chunk))
	if err != nil {
		return false, fmt.Errorf("sending input: %w", err)
	}

	return quit, nil
}

// endInput passes the end of the user's input on. On a pipe the input is over
// for good: that is recorded first, so a console opened from now on is told
// as it opens; then the console open now, if any, is told. It returns the id
// of the console told, or 0.
func endInput(out *sender, con *console) (uint32, error) {
	if !con.tty {
		con.inputEnded()
	}

	target, open := con.current()
	if !open {
		return 0, nil
	}

	err := out.send(eofReq(target))
	if err != nil {
		return 0, fmt.Errorf("sending end of input: %w", err)
	}

	return target, nil
}

// filterInput applies the escape sequence and local echo to keys read from a
// raw terminal; input from a pipe or a cooked terminal is forwarded as it is.
func (c *console) filterInput(esc *escapeFilter, keys []byte) ([]byte, bool) {
	if !c.rawActive() {
		return keys, false
	}

	chunk, quit, toggled := esc.apply(keys)
	if toggled {
		state := "off"
		if esc.echo {
			state = "on"
		}

		fmt.Fprintf(c.term, "\r\n[dutctl] local echo %s\r\n", state)
	}

	if esc.echo {
		localEcho(c.term, chunk)
	}

	return chunk, quit
}

// quitConsole ends the session on the user's escape sequence: the module is
// told its input ended and gets a moment to end the run on its own, which the
// serial bridge does within a second; a module that does not is cancelled, so
// quitting works against a stuck one as well.
func quitConsole(ctx context.Context, cancel context.CancelFunc, out *sender, con *console) {
	slog.Debug("send routine terminating", "reason", "escape sequence")

	if target, open := con.current(); open {
		_ = out.send(eofReq(target))
	}

	select {
	case <-ctx.Done():
	case <-time.After(quitGrace):
		cancel()
	}
}
