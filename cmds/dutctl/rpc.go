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
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/output"
	"github.com/BlindspotSoftware/dutctl/pkg/lock"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

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

//nolint:funlen,cyclop,gocognit,maintidx
func (app *application) runRPC(device, command string, cmdArgs []string) error {
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

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, numWorkers)

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
	go func() {
		const stdinBufSize = 256

		buf := make([]byte, stdinBufSize)

		var escapePending bool

		for {
			select {
			case <-runCtx.Done():
				log.Println("Send routine terminating: Run-Context cancelled")

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
				log.Println("Send routine terminating: escape sequence")
				cancel()

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
