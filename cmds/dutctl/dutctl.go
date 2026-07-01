// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// dutctl is the client application of the DUT Control system.
// It provides a command line interface to issue task on remote devices (DUTs).
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/buildinfo"
	"github.com/BlindspotSoftware/dutctl/internal/output"
	"github.com/BlindspotSoftware/dutctl/pkg/keyword"
	"github.com/BlindspotSoftware/dutctl/pkg/lock"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
)

const usageAbstract = `dutctl - The client application of the DUT Control system.
`
const usageSynopsis = `
SYNOPSIS:
	dutctl [options] list
	dutctl [options] <device>
	dutctl [options] <device> <command> [args...]
	dutctl [options] <device> <command> help
	dutctl [options] <device> lock [duration]
	dutctl [options] <device> unlock
	dutctl version

`
const usageDescription = `
If a device and a command are provided, dutctl will execute the command on the device.
The optional args are passed to the command.

To list all available devices, use the list command. If only a device is provided,
dutctl list all available commands for the device.

If a device, a command and the keyword help are provided, dutctl will show usage
information for the command.

The lock command reserves a device for the current user; the optional duration
(e.g. 30m, 2h) defaults to 30m. The unlock command releases it; pass the -force
option to release a lock held by another user.

`

// Usage strings for the command-line flags, shown in the OPTIONS section of `dutctl -h`.
const (
	serverAddrUsage   = `Address and port of the dutagent to connect to in the format: address:port`
	outputFormatUsage = `Output format, text|json|yaml|oneline, default is text`
	verboseUsage      = `Annotate output with connection/RPC context (metadata)`
	noColorUsage      = `Disable colored output`
	userUsage         = `User Identity of the user of the device, defaults to <user>@<host>`
	forceUsage        = `Force unlock a device locked by another user`
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
	fs.StringVar(&app.user, "u", lock.DefaultUser(), userUsage)
	fs.BoolVar(&app.force, "force", false, forceUsage)

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
	force             bool
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
	client := dutctlv1connect.NewDeviceServiceClient(
		newInsecureClient(),
		fmt.Sprintf("http://%s", app.serverAddr),
		connect.WithGRPC(),
	)

	app.rpcClient = client
}

func newInsecureClient() *http.Client {
	// Use the HTTP/2 protocol without TLS (h2c)
	transport := &http.Transport{}
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetUnencryptedHTTP2(true)

	return &http.Client{
		Transport: transport,
		// TODO: Don't forget timeouts! http.Client.Timeout must not be used here:
		// it bounds the entire exchange including the response body, which would
		// abort long-lived streaming RPCs. Instead use per-RPC context deadlines
		// on unary calls and/or transport timeouts (DialContext,
		// TLSHandshakeTimeout, ResponseHeaderTimeout, IdleConnTimeout).
	}
}

var errInvalidCmdline = fmt.Errorf("invalid command line")

// exitInterrupted is the conventional exit code for termination by a signal
// such as SIGINT (128 + signal number 2).
const exitInterrupted = 130

// start is the entry point of the application.
func (app *application) start() {
	if len(app.args) == 0 {
		app.exit(errInvalidCmdline)
	}

	if app.args[0] == "version" {
		app.printVersion()
		app.exit(nil)
	}

	app.setupRPCClient()

	if app.args[0] == "list" {
		if len(app.args) > 1 {
			app.exit(errInvalidCmdline)
		}

		err := app.listRPC()
		app.exit(err)
	}

	if len(app.args) == 1 {
		name := app.args[0]

		if handler, ok := keyword.DeviceHandler(name); ok {
			app.exit(handler(app, name))
		}

		app.exit(app.commandsRPC(name))
	}

	device := app.args[0]
	command := app.args[1]
	cmdArgs := app.args[2:]

	// Device-scoped keyword in the command slot, e.g. "dutctl <device> lock".
	if handler, ok := keyword.CommandHandler(command, keyword.CommandSlot); ok {
		app.exit(handler(app, device, command, cmdArgs))
	}

	// Keyword after a command, e.g. "dutctl <device> <command> help".
	if len(cmdArgs) > 0 {
		if handler, ok := keyword.CommandHandler(cmdArgs[0], keyword.AfterCommand); ok {
			app.exit(handler(app, device, command, cmdArgs))
		}
	}

	app.exit(app.runRPC(device, command, cmdArgs))
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
			_ = app.formatter.Flush()
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

		_ = app.formatter.Flush()
		app.exitFunc(exitInterrupted)

		return
	}

	// Render the terminating error through the formatter (stderr, format-aware).
	app.formatter.WriteContent(output.Content{
		Type:    output.TypeGeneral,
		Data:    err.Error(),
		IsError: true,
	})

	if errors.Is(err, errInvalidCmdline) {
		fmt.Fprint(app.stderr, usageSynopsis)
		app.printFlagDefaults()
	}

	_ = app.formatter.Flush()
	app.exitFunc(1)
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
