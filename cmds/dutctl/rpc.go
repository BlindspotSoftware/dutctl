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
	"github.com/BlindspotSoftware/dutctl/internal/buildinfo"
	"github.com/BlindspotSoftware/dutctl/internal/output"
	"github.com/BlindspotSoftware/dutctl/internal/rpc"
	"github.com/BlindspotSoftware/dutctl/pkg/headers"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"

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

// agentClient is one dutagent and the identity the client presents to it. It
// is a value, so a caller that holds one keeps talking to the same agent as
// whoever changes theirs. Its unary calls return the agent's answer as data;
// the command line's xRPC methods print it.
type agentClient struct {
	addr   string // the completed dial address, named in the output's metadata
	user   string // sent with the calls that act for the user: Run, Lock and Unlock
	client dutctlv1connect.DeviceServiceClient
}

func newAgentClient(addr, user string) agentClient {
	return agentClient{
		addr: addr,
		user: user,
		client: rpc.NewDeviceClient(
			addr,
			connect.WithInterceptors(rpc.NewVersionAdvisor(buildinfo.Version)),
		),
	}
}

// agent returns the agent the command line talks to.
func (app *application) agent() agentClient {
	return agentClient{addr: app.serverAddr, user: app.user, client: app.rpcClient}
}

func (app *application) listRPC(ctx context.Context) error {
	devices, err := app.agent().listDevices(ctx)
	if err != nil {
		return err
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

// listDevices returns the agent's devices with their lock state.
func (ac agentClient) listDevices(ctx context.Context) ([]output.DeviceEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, unaryTimeout)
	defer cancel()

	res, err := ac.client.List(ctx, connect.NewRequest(&pb.ListRequest{}))
	if err != nil {
		return nil, err
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

	return devices, nil
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

	entry, err := app.agent().lockDevice(ctx, device, duration)
	if err != nil {
		return err
	}

	app.formatter.WriteContent(output.Content{
		Type: output.TypeLockResult,
		Data: entry,
		Metadata: map[string]string{
			output.MetaServer: app.serverAddr,
			output.MetaMsg:    "Lock Response",
		},
	})

	return nil
}

// lockDevice reserves device for the user for duration, 0 for the agent's
// default, and returns the lock the agent granted.
func (ac agentClient) lockDevice(ctx context.Context, device string, duration time.Duration) (output.DeviceEntry, error) {
	if duration > longLockWarnThreshold {
		slog.Warn("requested a long lock duration; release the device when you are done", "duration", duration)
	}

	ctx, cancel := context.WithTimeout(ctx, unaryTimeout)
	defer cancel()

	req := connect.NewRequest(&pb.LockRequest{
		Device:          device,
		DurationSeconds: int64(duration.Seconds()),
	})
	req.Header().Set(headers.User, ac.user)

	res, err := ac.client.Lock(ctx, req)
	if err != nil {
		return output.DeviceEntry{}, err
	}

	return output.DeviceEntry{
		Name:      res.Msg.GetDevice(),
		Locked:    true,
		Owner:     res.Msg.GetLock().GetOwner(),
		ExpiresAt: res.Msg.GetLock().GetExpiresAt(),
	}, nil
}

func (app *application) unlockRPC(ctx context.Context, device string, force bool) error {
	err := app.agent().unlockDevice(ctx, device, force)
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

// unlockDevice releases the user's reservation of device; force releases
// another user's as well.
func (ac agentClient) unlockDevice(ctx context.Context, device string, force bool) error {
	ctx, cancel := context.WithTimeout(ctx, unaryTimeout)
	defer cancel()

	req := connect.NewRequest(&pb.UnlockRequest{Device: device, Force: force})
	req.Header().Set(headers.User, ac.user)

	_, err := ac.client.Unlock(ctx, req)

	return err
}

func (app *application) commandsRPC(ctx context.Context, device string) error {
	commands, err := app.agent().deviceCommands(ctx, device)
	if err != nil {
		return err
	}

	app.formatter.WriteContent(output.Content{
		Type: output.TypeCommandList,
		Data: commands,
		Metadata: map[string]string{
			output.MetaServer: app.serverAddr,
			output.MetaMsg:    "Commands Response",
			output.MetaDevice: device,
		},
	})

	return nil
}

// deviceCommands returns the names of device's commands.
func (ac agentClient) deviceCommands(ctx context.Context, device string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, unaryTimeout)
	defer cancel()

	res, err := ac.client.Commands(ctx, connect.NewRequest(&pb.CommandsRequest{Device: device}))
	if err != nil {
		return nil, err
	}

	return res.Msg.GetCommands(), nil
}

func (app *application) detailsRPC(ctx context.Context, device, command, keyword string) error {
	details, err := app.agent().commandDetails(ctx, device, command, keyword)
	if err != nil {
		return err
	}

	app.formatter.WriteContent(output.Content{
		Type: output.TypeCommandDetail,
		Data: details,
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

// commandDetails returns the agent's text on command of device for keyword,
// the usage for keyword.Help.
func (ac agentClient) commandDetails(ctx context.Context, device, command, keyword string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, unaryTimeout)
	defer cancel()

	res, err := ac.client.Details(ctx, connect.NewRequest(&pb.DetailsRequest{
		Device:  device,
		Command: command,
		Keyword: keyword,
	}))
	if err != nil {
		return "", err
	}

	return res.Msg.GetDetails(), nil
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

// run is one Run call in flight: the stream, the console and what the two
// routines that drive them, receiveLoop and sendLoop, share.
type run struct {
	app      *application
	stream   runStream
	console  *console
	metadata map[string]string

	// sendMu serialises the sends: connect allows one Send at a time on a
	// stream, and both routines send, the receive routine file replies and
	// the send routine input.
	sendMu sync.Mutex

	// cancel ends the run, which both routines then leave.
	cancel context.CancelFunc
	// errCh takes the failure that ends a routine, with room for both.
	errCh chan error
}

// send sends req on the stream, one send at a time.
func (r *run) send(req *pb.RunRequest) error {
	r.sendMu.Lock()
	defer r.sendMu.Unlock()

	return r.stream.Send(req)
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

	// Raw mode ends with the run on every path: a normal end, the escape
	// sequence, a signal, a lost connection.
	defer app.console.finish()

	stream := app.rpcClient.Run(runCtx)
	stream.RequestHeader().Set(headers.User, app.user)

	r := &run{
		app:     app,
		stream:  stream,
		console: app.console,
		metadata: map[string]string{
			output.MetaServer:  app.serverAddr,
			output.MetaMsg:     "Run Response",
			output.MetaDevice:  device,
			output.MetaCommand: command,
			output.MetaArgs:    strings.Join(cmdArgs, " "),
		},
		cancel: cancelRunCtx,
		errCh:  make(chan error, numWorkers),
	}

	err := r.sendCommand(device, command, cmdArgs)
	if err != nil {
		return err
	}

	go r.receiveLoop(runCtx)
	go r.sendLoop(runCtx)

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
	case err := <-r.errCh:
		return err
	}
}

// sendCommand starts the run on the agent. Connect reports a stream that
// failed, to an agent that cannot be reached for one, as io.EOF on Send;
// Receive tells why, so the caller gets the agent's code.
func (r *run) sendCommand(device, command string, cmdArgs []string) error {
	err := r.send(&pb.RunRequest{
		Msg: &pb.RunRequest_Command{
			Command: &pb.Command{
				Device:  device,
				Command: command,
				Args:    cmdArgs,
			},
		},
	})
	if errors.Is(err, io.EOF) {
		_, err = r.stream.Receive()
	}

	return err
}

// receiveLoop is the receive routine: it renders the agent's responses, drives
// the console with the agent's events and answers file requests, until the
// stream ends. It cancels the run when it returns, so a run never outlives
// its stream.
//
//nolint:funlen,cyclop // one switch over every kind of response; inherently branchy
func (r *run) receiveLoop(ctx context.Context) {
	defer r.cancel()

	for {
		select {
		case <-ctx.Done():
			slog.Debug("receive routine terminating", "reason", "run-context cancelled")

			return
		default: // Unblock select, continue with the forwarding logic.
		}

		res, err := r.stream.Receive()

		switch {
		case errors.Is(err, io.EOF):
			slog.Debug("receive routine terminating", "reason", "stream closed by agent")

			return
		case err != nil && (errors.Is(err, context.Canceled) || connect.CodeOf(err) == connect.CodeCanceled):
			slog.Debug("receive routine terminating", "reason", "context cancelled")

			return
		case err != nil:
			r.errCh <- fmt.Errorf("receiving RPC message: %w", err)

			return
		}

		//nolint:protogetter
		switch msg := res.Msg.(type) {
		case *pb.RunResponse_Print:
			r.app.formatter.WriteContent(output.Content{
				Type:     output.TypeModuleOutput,
				Data:     string(msg.Print.GetText()),
				Metadata: r.metadata,
			})
		case *pb.RunResponse_ConsoleOutput:
			r.consoleOutput(msg.ConsoleOutput)
		case *pb.RunResponse_ConsoleOpen:
			if r.console.opened(msg.ConsoleOpen.GetId(), msg.ConsoleOpen.GetMode()) {
				// The user's input ended before this console opened: say so now.
				// A send that fails because the stream is over is not the run's
				// error: the next Receive reports how the agent ended the run.
				err = r.send(eofReq(msg.ConsoleOpen.GetId()))
				if err != nil && !errors.Is(err, io.EOF) {
					r.errCh <- fmt.Errorf("sending end of input: %w", err)

					return
				}
			}
		case *pb.RunResponse_ConsoleClose:
			r.console.closed()
		case *pb.RunResponse_FileRequest:
			path := msg.FileRequest.GetPath()
			slog.Debug("file requested by agent", "path", path)

			content, err := os.ReadFile(path)
			if err != nil {
				r.errCh <- fmt.Errorf("reading requested file %q: %w", path, err)

				return
			}

			err = r.send(&pb.RunRequest{
				Msg: &pb.RunRequest_File{
					File: &pb.File{
						Path:    path,
						Content: content,
					},
				},
			})
			if err != nil && !errors.Is(err, io.EOF) {
				r.errCh <- fmt.Errorf("sending requested file %q: %w", path, err)

				return
			}

			r.app.formatter.WriteContent(output.Content{
				Type:     output.TypeFileTransfer,
				Data:     output.FileTransfer{Direction: "sent", Path: path, Bytes: len(content)},
				Metadata: r.metadata,
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
				r.errCh <- fmt.Errorf("saving received file %q: %w", path, err)

				return
			}

			r.app.formatter.WriteContent(output.Content{
				Type:     output.TypeFileTransfer,
				Data:     output.FileTransfer{Direction: "received", Path: path, Bytes: len(content)},
				Metadata: r.metadata,
			})

		default:
			slog.Warn("unexpected message type", "type", fmt.Sprintf("%T", msg))
		}
	}
}

// consoleOutput shows the module's console output: on a raw terminal as it
// is, otherwise through the formatter like Print output. The module's standard
// error goes to the client's.
func (r *run) consoleOutput(msg *pb.ConsoleOutput) {
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

	if r.console.output(data, isErr) {
		return
	}

	r.app.formatter.WriteContent(output.Content{
		Type:     output.TypeModuleOutput,
		Data:     string(data),
		IsError:  isErr,
		Metadata: r.metadata,
	})
}

// sendLoop is the send routine: it forwards the user's input to the console
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
func (r *run) sendLoop(ctx context.Context) {
	buf := make([]byte, inputChunk)

	var (
		esc    = escapeFilter{term: r.console.term}
		ended  uint32 // the console whose input ended, on a terminal
		filled uint32 // the console the escape filter's state belongs to
	)

	for {
		if _, ok := r.console.await(ctx, ended); !ok {
			return
		}

		count, err := r.console.stdin.Read(buf)
		if count > 0 {
			quit, sendErr := r.handleInput(ctx, &esc, &filled, buf[:count])
			if sendErr != nil {
				r.reportSendError(sendErr)

				return
			}

			if quit {
				r.quitConsole(ctx)

				return
			}
		}

		if err == nil {
			continue
		}

		if !errors.Is(err, io.EOF) {
			r.errCh <- fmt.Errorf("reading stdin: %w", err)

			return
		}

		target, sendErr := r.endInput()
		if sendErr != nil {
			r.reportSendError(sendErr)

			return
		}

		if !r.console.tty {
			return
		}

		ended = target
	}
}

// reportSendError passes a failed send on as the run's error, unless the
// stream is simply over: connect reports that as io.EOF on Send, and the
// receive routine then learns how the agent ended the run and reports that.
func (r *run) reportSendError(err error) {
	if errors.Is(err, io.EOF) {
		slog.Debug("send routine terminating", "reason", "stream closed")

		return
	}

	r.errCh <- err
}

// handleInput forwards keys to the console open now, waiting for one if the
// console closed while the keys were read. A pending escape prefix does not
// carry over into another console: the filter starts afresh for each.
func (r *run) handleInput(ctx context.Context, esc *escapeFilter, filled *uint32, keys []byte) (bool, error) {
	target, ok := r.console.await(ctx, 0)
	if !ok {
		return false, nil
	}

	if target != *filled {
		*esc = escapeFilter{term: esc.term}
		*filled = target
	}

	return r.forwardInput(target, esc, keys)
}

// forwardInput sends keys, filtered on a raw terminal, to the console target,
// and reports whether the quit sequence was seen.
func (r *run) forwardInput(target uint32, esc *escapeFilter, keys []byte) (bool, error) {
	chunk, quit := r.console.filterInput(esc, keys)
	if len(chunk) == 0 {
		return quit, nil
	}

	err := r.send(inputReq(target, chunk))
	if err != nil {
		return false, fmt.Errorf("sending input: %w", err)
	}

	return quit, nil
}

// endInput passes the end of the user's input on. On a pipe the input is over
// for good: that is recorded first, so a console opened from now on is told
// as it opens; then the console open now, if any, is told. It returns the id
// of the console told, or 0.
func (r *run) endInput() (uint32, error) {
	if !r.console.tty {
		r.console.inputEnded()
	}

	target, open := r.console.current()
	if !open {
		return 0, nil
	}

	err := r.send(eofReq(target))
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

	return esc.apply(keys)
}

// quitConsole ends the session on the user's escape sequence: the module is
// told its input ended and gets a moment to end the run on its own, which the
// serial bridge does within a second; a module that does not is cancelled, so
// quitting works against a stuck one as well.
func (r *run) quitConsole(ctx context.Context) {
	slog.Debug("send routine terminating", "reason", "escape sequence")

	if target, open := r.console.current(); open {
		_ = r.send(eofReq(target))
	}

	select {
	case <-ctx.Done():
	case <-time.After(quitGrace):
		r.cancel()
	}
}
