// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// dutctl is the client application of the DUT Control system.
// It provides a command line interface to issue tasks on remote devices (DUTs).
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/auth"
	"github.com/BlindspotSoftware/dutctl/internal/buildinfo"
	"github.com/BlindspotSoftware/dutctl/internal/output"
	"github.com/BlindspotSoftware/dutctl/internal/rpc"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
)

const usageAbstract = `dutctl - The client application of the DUT Control system.
`

const usageSynopsis = `
SYNOPSIS:
	dutctl [options] [list]
	dutctl [options] <device>
	dutctl [options] <device> <command> [args...]
	dutctl [options] <device> <command> help
	dutctl [options] <device> lock [duration]
	dutctl [options] <device> unlock [force]
	dutctl version

`

const usageDescription = `
If a device and a command are provided, dutctl will execute the command on the device.
The optional args are passed to the command.

To list all available devices, use the list command. If only a device is provided,
dutctl list all available commands for the device.

If a device, a command and the keyword help are provided, dutctl will show usage
information for the command.

The lock command reserves a device for the current user for an optional duration
(e.g. 30m, 2h); when omitted, the agent applies a default. The unlock command
releases it; add the force keyword to release a lock held by another user.
Locks are advisory, so reserve a device only as long as you need it.

When dutctl is run without any positional arguments, it defaults to the list command.
`

// Usage strings for the command-line flags, shown in the OPTIONS section of dutctl -h.
const (
	serverAddrUsage   = `Address and port of the dutagent to connect to in the format: address:port`
	outputFormatUsage = `Output format, text|json|yaml|oneline, default is text`
	verboseUsage      = `Annotate output with connection/RPC context (metadata)`
	noColorUsage      = `Disable colored output`
	userUsage         = `User Identity of the user of the device, defaults to <user>@<host>`
	logUsage          = `Client-side diagnostic logging (on stderr), debug|warn|none, default is warn`
)

func newApp(stdin io.Reader, stdout, stderr io.Writer, exitFunc func(int), args []string) *application {
	var app application

	app.stdout = stdout
	app.stderr = stderr
	app.stdin = stdin
	app.exitFunc = exitFunc

	fs := flag.NewFlagSet(args[0], flag.ExitOnError)
	fs.SetOutput(stderr)

	app.printFlagDefaults = func() {
		fmt.Fprint(stderr, "OPTIONS:\n")
		fs.PrintDefaults()
	}
	fs.Usage = func() {
		fmt.Fprint(stderr, usageAbstract, usageSynopsis, usageDescription)
		app.printFlagDefaults()
	}
	// Flags
	fs.StringVar(&app.serverAddr, "s", "localhost:1024", serverAddrUsage)
	fs.StringVar(&app.outputFormat, "f", "", outputFormatUsage)
	fs.BoolVar(&app.verbose, "v", false, verboseUsage)
	fs.BoolVar(&app.noColor, "no-color", false, noColorUsage)
	fs.StringVar(&app.user, "u", auth.Default().User(), userUsage)

	mode := logModeWarn
	fs.Var(&mode, "log", logUsage)

	//nolint:errcheck // flag.Parse always returns no error because of flag.ExitOnError
	fs.Parse(args[1:])
	app.args = fs.Args()

	// Setup diagnostic logging. The handler writes to stderr only and is
	// installed as the process default so any package can log via package-level
	// slog. An invalid --log value was already rejected by fs.Parse above.
	//
	// Color is suppressed unless -no-color is unset AND the target stream is a
	// terminal, so redirected/piped output stays free of ANSI escapes. The log
	// handler is gated on stderr; the formatter's content on stdout.
	app.logHandler = newCLIHandler(stderr, mode, !app.noColor && isTerminal(stderr))
	slog.SetDefault(slog.New(app.logHandler))

	// Setup output formatter
	app.formatter = output.New(output.Config{
		Stdout:  stdout,
		Stderr:  stderr,
		Format:  app.outputFormat,
		Verbose: app.verbose,
		NoColor: app.noColor || !isTerminal(stdout),
	})

	return &app
}

type application struct {
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
	exitFunc func(int)

	// flags
	serverAddr        string
	outputFormat      string
	verbose           bool
	noColor           bool
	user              string
	args              []string
	printFlagDefaults func()

	// runtime services
	rpcClient dutctlv1connect.DeviceServiceClient
	formatter output.Formatter
	// logHandler is retained only so exit can call Flush: diagnostics are emitted
	// via package-level slog (this handler is the process default), but the
	// buffered warning summary must be flushed explicitly and Flush is not part
	// of the slog.Handler interface reachable through slog.Default().
	logHandler *cliHandler
}

func (app *application) setupRPCClient() {
	app.rpcClient = rpc.NewDeviceClient(
		app.serverAddr,
		connect.WithInterceptors(rpc.NewVersionAdvisor(buildinfo.Version)),
	)
}

// errInvalidCmdline is returned by dispatch for a malformed command line.
// exit() matches it with errors.Is and renders the usage synopsis alongside
// the error message.
var errInvalidCmdline = fmt.Errorf("invalid command line")

// exitInterrupted is the conventional exit code for termination by a signal
// such as SIGINT (128 + signal number 2).
const exitInterrupted = 130

// start is the entry point of the application.
func (app *application) start() {
	if len(app.args) > 0 && app.args[0] == "version" {
		app.printVersion()
		app.exit(nil)
	}

	app.setupRPCClient()
	app.exit(app.dispatch())
}

// dispatch decides which RPC to call based on app.args.
// It is split out from start so it can be unit tested without os.Exit.
//
// It returns errInvalidCmdline for a malformed command line (exit() renders it
// with the usage synopsis), or the error returned by the dispatched RPC.
func (app *application) dispatch() error {
	if len(app.args) == 0 {
		return app.listRPC()
	}

	if app.args[0] == "list" {
		if len(app.args) > 1 {
			return errInvalidCmdline
		}

		return app.listRPC()
	}

	if len(app.args) == 1 {
		return app.commandsRPC(app.args[0])
	}

	return app.dispatchCommand(app.args[0], app.args[1], app.args[2:])
}

// dispatchCommand handles the "<device> <command> [args...]" forms: the built-in
// lock/unlock keywords, the help keyword, and otherwise a module run. It returns
// errInvalidCmdline for a malformed invocation.
func (app *application) dispatchCommand(device, command string, cmdArgs []string) error {
	switch command {
	case "lock":
		// lock takes an optional single duration argument.
		if len(cmdArgs) > 1 {
			return errInvalidCmdline
		}

		return app.lockRPC(device, cmdArgs)
	case "unlock":
		// unlock takes nothing, or the single keyword "force".
		force, err := parseUnlockArgs(cmdArgs)
		if err != nil {
			return err
		}

		return app.unlockRPC(device, force)
	}

	// help is a keyword only as the sole argument: "<device> <command> help".
	// Trailing arguments after it are a malformed command line, not silently
	// dropped.
	if len(cmdArgs) > 0 && cmdArgs[0] == "help" {
		if len(cmdArgs) > 1 {
			return errInvalidCmdline
		}

		return app.detailsRPC(device, command, "help")
	}

	return app.runRPC(device, command, cmdArgs)
}

// parseUnlockArgs interprets the arguments to the unlock command. Unlock accepts
// no arguments for a normal, owner-only release, or the single keyword "force"
// to break a lock held by another user. Any other argument is a command-line
// error (errInvalidCmdline), which renders the usage synopsis.
func parseUnlockArgs(cmdArgs []string) (bool, error) {
	switch {
	case len(cmdArgs) == 0:
		return false, nil
	case len(cmdArgs) == 1 && cmdArgs[0] == "force":
		return true, nil
	default:
		return false, errInvalidCmdline
	}
}

// exit terminates the application. Buffered diagnostics (the warning summary)
// are flushed first so they read as a trailing note, then any terminating
// status or error is rendered through the formatter as the final output. A nil
// error exits 0; an interrupt exits 130; any other error exits 1.
func (app *application) exit(err error) {
	if app.logHandler != nil {
		app.logHandler.Flush()
	}

	if err == nil {
		if app.formatter != nil {
			app.formatter.Flush()
		}

		app.exitFunc(0)

		return
	}

	if errors.Is(err, errInterrupted) {
		app.formatter.WriteContent(output.Content{
			Type:    output.TypeGeneral,
			Data:    "interrupted",
			IsError: true,
		})

		app.formatter.Flush()
		app.exitFunc(exitInterrupted)

		return
	}

	// Render the terminating error through the formatter (stderr, format-aware).
	app.formatter.WriteContent(output.Content{
		Type:    output.TypeGeneral,
		Data:    userFacingError(err, app.serverAddr),
		IsError: true,
	})

	if errors.Is(err, errInvalidCmdline) {
		fmt.Fprint(app.stderr, usageSynopsis)
		app.printFlagDefaults()
	}

	app.formatter.Flush()
	app.exitFunc(1)
}

// userFacingError renders err for display. For a connect RPC error it drops the
// gRPC status-code prefix that err.Error() carries (e.g. "unavailable: ...") and
// gives the common "agent unreachable" case a friendlier line; other errors —
// including client-side ones like a bad command line or a missing local file —
// render unchanged.
func userFacingError(err error, serverAddr string) string {
	var connErr *connect.Error
	if !errors.As(err, &connErr) {
		return err.Error()
	}

	if connErr.Code() == connect.CodeUnavailable {
		return fmt.Sprintf("cannot reach dutagent at %s (%s)", serverAddr, connErr.Message())
	}

	return connErr.Message()
}

func (app *application) printVersion() {
	app.formatter.WriteContent(output.Content{
		Type: output.TypeVersion,
		Data: "DUT Control Client\n" + buildinfo.VersionString(),
	})
}

func main() {
	newApp(os.Stdin, os.Stdout, os.Stderr, os.Exit, os.Args).start()
}
