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
	"sync/atomic"
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

// rawConsole lazily switches the terminal to raw input mode, and prints the
// interactive session banner, the first time the agent actually streams console
// output. One-shot commands (power, flash) only ever send Print messages, so
// they never arm it: the terminal is left untouched and no misleading banner is
// shown. A serial session sends its "Connected" banner as console output within
// milliseconds, which arms it just in time for keystroke forwarding.
//
// The zero value is not usable; build one with newRawConsole.
type rawConsole struct {
	fd      int         // stdin file descriptor (valid only when canRaw is true)
	canRaw  bool        // stdin is an *os.File, so raw mode may be attempted
	once    sync.Once   // guards the one-time arm
	active  atomic.Bool // true once raw mode is on; read by the send goroutine
	mu      sync.Mutex  // guards restore
	restore func()      // set by arm, called by disarm; nil until/unless armed
}

// newRawConsole returns a console that may switch the terminal to raw mode only
// when both: interactive is true (the command was invoked without arguments, so
// it is a hand-driven session rather than a scripted one) and stdin is a real
// *os.File. A pipe, /dev/null, or any argument-bearing (scripted) invocation
// yields canRaw=false, so it never changes the terminal nor prints the banner.
func newRawConsole(stdin io.Reader, interactive bool) *rawConsole {
	console := &rawConsole{}

	if f, ok := stdin.(*os.File); ok && interactive {
		console.fd = int(f.Fd())
		console.canRaw = true
	}

	return console
}

// arm switches the terminal to raw mode and prints the session banner on its
// first call; later calls are no-ops. setRawInput returns nil for a non-terminal
// fd, so arming is silently skipped when stdin is not a TTY. Safe to call from
// any goroutine.
func (rc *rawConsole) arm() {
	rc.once.Do(func() {
		if !rc.canRaw {
			return
		}

		restore := setRawInput(rc.fd)
		if restore == nil {
			return // not a terminal — keep input line-buffered
		}

		rc.mu.Lock()
		rc.restore = restore
		rc.mu.Unlock()

		rc.active.Store(true)

		// The escape sequence is the only way to quit while raw mode is on.
		fmt.Fprint(os.Stderr, "\r\n[dutctl] interactive session — press Ctrl-A then x to quit\r\n")
	})
}

// isActive reports whether raw mode is currently engaged. The send goroutine
// uses it to decide whether to apply the Ctrl-A escape filter to stdin.
func (rc *rawConsole) isActive() bool {
	return rc.active.Load()
}

// disarm restores the terminal if arm switched it to raw mode. It is safe to
// defer unconditionally and safe to call when arm never fired.
func (rc *rawConsole) disarm() {
	rc.mu.Lock()
	restore := rc.restore
	rc.restore = nil
	rc.mu.Unlock()

	if restore != nil {
		restore()
	}
}

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
			"server": app.serverAddr,
			"msg":    "List Response",
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
			"server": app.serverAddr,
			"msg":    "Lock Response",
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
			"server": app.serverAddr,
			"msg":    "Unlock Response",
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
			"server": app.serverAddr,
			"msg":    "Commands Response",
			"device": device,
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
			"server":  app.serverAddr,
			"rpc":     "Details Response",
			"device":  device,
			"command": command,
			"keyword": keyword,
		},
	})

	return nil
}

// Interactive escape sequence. Ctrl-A is the prefix; the following key decides:
//   - 'x'/'X'/Ctrl-X: quit dutctl.
//   - Ctrl-A: send a single literal Ctrl-A to the DUT.
//   - anything else: forward both the prefix and the key unchanged.
//
// Every other byte (Ctrl-C, Ctrl-D, Ctrl-Z, ...) is forwarded to the DUT.
const (
	escapePrefix   = 0x01 // Ctrl-A
	escapeQuitCtrl = 0x18 // Ctrl-X
)

// filterEscape applies the interactive escape state machine to in. It returns
// the bytes to forward to the DUT and whether the quit sequence was seen.
// escapePending carries the "prefix seen" state across reads, so the prefix and
// its following key may arrive in separate stdin reads.
func filterEscape(in []byte, escapePending *bool) ([]byte, bool) {
	out := make([]byte, 0, len(in))

	for _, char := range in {
		if *escapePending {
			*escapePending = false

			switch char {
			case 'x', 'X', escapeQuitCtrl:
				return out, true
			case escapePrefix: // prefix pressed twice -> send one literal Ctrl-A
				out = append(out, escapePrefix)
			default: // not an escape; forward the prefix and this byte
				out = append(out, escapePrefix, char)
			}

			continue
		}

		if char == escapePrefix {
			*escapePending = true

			continue
		}

		out = append(out, char)
	}

	return out, false
}

// runRPC executes command on device, streaming module output and forwarding
// stdin and file transfers until the run ends. It returns nil on normal
// completion, errInterrupted when a signal (Ctrl-C) ended the run, or a wrapped
// error from a worker goroutine (stream send/receive or file I/O). A connect
// status from the agent surfaces through the returned error; exit() renders it.
//
//nolint:funlen,cyclop,gocognit,gocyclo,maintidx // two streaming workers plus raw-console handling; inherently branchy
func (app *application) runRPC(ctx context.Context, device, command string, cmdArgs []string) error {
	const numWorkers = 2 // The send and receive worker goroutines

	// Raw input mode (no echo, no canonical line buffering, no local signal
	// generation) is needed only for a live, hand-driven console session, so that
	// each keystroke — including Ctrl-C — is forwarded to the DUT immediately and
	// echoed by the remote side. Two conditions must hold, so it is armed lazily:
	//
	//   - The invocation is interactive, i.e. the command was given no arguments.
	//     A command with arguments is parameterised/scripted (e.g. a serial
	//     expect/send sequence the agent drives on its own); raw mode there would
	//     only mislead and would steal Ctrl-C from aborting the client.
	//   - The agent actually streams console output, which one-shot commands
	//     (power, flash) never do — so they leave the terminal untouched.
	//
	// Piped/scripted stdin is never switched to raw mode and is forwarded verbatim.
	console := newRawConsole(app.stdin, len(cmdArgs) == 0)
	defer console.disarm()

	// ctx is the shared signal context from dispatch, cancelled on SIGINT/SIGTERM
	// so Ctrl-C terminates gracefully (running the normal teardown and flushing the
	// warning summary) instead of killing the process. A stream has no overall
	// deadline. runCtx is the child the workers cancel on completion, including on
	// the interactive quit sequence (Ctrl-A x).
	runCtx, cancelRunCtx := context.WithCancel(ctx)
	defer cancelRunCtx()

	errChan := make(chan error, numWorkers)

	stream := app.rpcClient.Run(runCtx)
	stream.RequestHeader().Set(headers.User, app.user)

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
				// First console output means this is a live console session:
				// switch the terminal to raw mode now (no-op for non-TTY stdin)
				// so keystrokes forward correctly and the banner is truthful.
				console.arm()

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
			case *pb.RunResponse_FileRequest:
				path := msg.FileRequest.GetPath()
				slog.Debug("file requested by agent", "path", path)

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
	}()

	// Send routine — reads raw bytes from stdin and forwards them to the server.
	//
	// Unlike the receive routine this goroutine intentionally does NOT defer
	// cancel() for the EOF case. When stdin reaches EOF (e.g. /dev/null in
	// non-interactive runs) this goroutine returns immediately; if it cancelled
	// the context on exit, the receive routine would be torn down before it
	// could read and print the server's response.
	//
	// It DOES cancel when the user types the interactive escape sequence
	// (Ctrl-A x): that is an explicit request to end the session.
	//
	// app.stdin.Read is not interruptible by runCtx, so on a normal one-shot
	// completion this goroutine stays blocked in Read after runRPC returns and is
	// only reclaimed when the process exits. That is acceptable here because dutctl
	// runs one command per invocation and exits immediately; a longer-lived caller
	// of runRPC would need a closeable stdin to reclaim it.
	go func() {
		const stdinBufSize = 256

		buf := make([]byte, stdinBufSize)

		var escapePending bool

		for {
			select {
			case <-runCtx.Done():
				slog.Debug("send routine terminating", "reason", "run-context cancelled")

				return
			default:
			}

			nRead, err := app.stdin.Read(buf)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					errChan <- fmt.Errorf("reading stdin: %w", err)
				}

				return
			}

			payload := buf[:nRead]

			quit := false
			if console.isActive() {
				// Intercept the escape sequence; forward everything else
				// (including Ctrl-C) untouched.
				payload, quit = filterEscape(payload, &escapePending)
			}

			if len(payload) > 0 {
				sendErr := stream.Send(&pb.RunRequest{
					Msg: &pb.RunRequest_Console{
						Console: &pb.Console{
							Data: &pb.Console_Stdin{
								Stdin: payload,
							},
						},
					},
				})
				if sendErr != nil {
					errChan <- fmt.Errorf("sending RPC message: %w", sendErr)

					return
				}
			}

			if quit {
				slog.Debug("send routine terminating", "reason", "escape sequence")
				cancelRunCtx()

				return
			}
		}
	}()

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
