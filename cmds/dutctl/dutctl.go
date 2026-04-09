// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// dutctl is the client application of the DUT Control system.
// It provides a command line interface to issue task on remote devices (DUTs).
package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/buildinfo"
	"github.com/BlindspotSoftware/dutctl/internal/output"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
	"golang.org/x/net/http2"
)

const usageAbstract = `dutctl - The client application of the DUT Control system.
`

const usageSynopsis = `
SYNOPSIS:
	dutctl [options] list
	dutctl [options] <device>
	dutctl [options] <device> lock [duration]
	dutctl [options] <device> unlock
	dutctl [options] <device> status
	dutctl [options] <device> <command> [args...]
	dutctl [options] <device> <command> help
	dutctl version

`

const usageDescription = `
If a device and a command are provided, dutctl will execute the command on the device.
The optional args are passed to the command.

To list all available devices, use the list command. If only a device is provided,
dutctl list all available commands for the device.

If a device, a command and the keyword help are provided, dutctl will show usage 
information for the command.

`

const (
	serverAddrInfo   = `Address and port of the dutagent to connect to in the format: address:port`
	outputFormatInfo = `Output format, text|json|yaml|oneline, default is text`
	verboseInfo      = `Verbose output`
	noColorInfo      = `Disable colored output`
	ownerInfo        = `User identity for lock operations (default: $USER@$HOSTNAME)`
)

const defaultLockDuration = 5 * time.Minute

// defaultOwner returns "$USER@$HOSTNAME" as the default owner identity.
func defaultOwner() string {
	user := os.Getenv("USER")
	if user == "" {
		user = "unknown"
	}

	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}

	return user + "@" + host
}

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
	fs.StringVar(&app.serverAddr, "s", "localhost:1024", serverAddrInfo)
	fs.StringVar(&app.outputFormat, "f", "", outputFormatInfo)
	fs.BoolVar(&app.verbose, "v", false, verboseInfo)
	fs.BoolVar(&app.noColor, "no-color", false, noColorInfo)
	fs.StringVar(&app.owner, "u", defaultOwner(), ownerInfo)

	//nolint:errcheck // flag.Parse always returns no error because of flag.ExitOnError
	fs.Parse(args[1:])
	app.args = fs.Args()

	// Setup output formatter
	app.formatter = output.New(output.Config{
		Stdout:  stdout,
		Stderr:  stderr,
		Format:  app.outputFormat,
		Verbose: app.verbose,
		NoColor: app.noColor,
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
	owner             string
	args              []string
	printFlagDefaults func()

	rpcClient dutctlv1connect.DeviceServiceClient
	formatter output.Formatter
}

func (app *application) setupRPCClient() {
	client := dutctlv1connect.NewDeviceServiceClient(
		// Instead of http.DefaultClient, use the HTTP/2 protocol without TLS
		newInsecureClient(),
		fmt.Sprintf("http://%s", app.serverAddr),
		connect.WithGRPC(),
	)

	app.rpcClient = client
}

func newInsecureClient() *http.Client {
	return &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				// If you're also using this client for non-h2c traffic, you may want
				// to delegate to tls.Dial if the network isn't TCP or the addr isn't
				// in an allowlist.

				//nolint:noctx
				return net.Dial(network, addr)
			},
			// Don't forget timeouts!
		},
	}
}

var errInvalidCmdline = fmt.Errorf("invalid command line")

// start is the entry point of the application.
func (app *application) start() {
	log.SetOutput(app.stdout)

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
		device := app.args[0]
		err := app.commandsRPC(device)
		app.exit(err)
	}

	device := app.args[0]
	command := app.args[1]
	cmdArgs := app.args[2:]

	app.runCommand(device, command, cmdArgs)
}

// runCommand dispatches device commands, including the built-in lock/unlock/status
// meta-commands and regular module commands.
func (app *application) runCommand(device, command string, cmdArgs []string) {
	switch command {
	case "lock":
		app.exit(app.runLock(device, cmdArgs))

		return
	case "unlock":
		app.exit(app.unlockRPC(device))

		return
	case "status":
		app.exit(app.lockStatusRPC(device))

		return
	}

	if len(cmdArgs) > 0 && cmdArgs[0] == "help" {
		app.exit(app.detailsRPC(device, command, "help"))
	}

	app.exit(app.runRPC(device, command, cmdArgs))
}

// runLock parses an optional duration argument and calls lockRPC.
func (app *application) runLock(device string, cmdArgs []string) error {
	duration := defaultLockDuration

	if len(cmdArgs) > 1 {
		return fmt.Errorf("%w: lock takes at most one argument (duration), got %d", errInvalidCmdline, len(cmdArgs))
	}

	if len(cmdArgs) == 1 {
		var parseErr error

		duration, parseErr = time.ParseDuration(cmdArgs[0])
		if parseErr != nil {
			return fmt.Errorf("%w: invalid duration %q: %v", errInvalidCmdline, cmdArgs[0], parseErr)
		}
	}

	return app.lockRPC(device, duration)
}

// exit terminates the application. If the provided error is not nil, it is printed to
// the standard error output. If printUsage is true, the usage information is printed additionally.
func (app *application) exit(err error) {
	if err == nil {
		// Flush any buffered output before exiting
		if app.formatter != nil {
			_ = app.formatter.Flush()
		}

		app.exitFunc(0)
	}

	if err != nil {
		log.Print(err)
	}

	if errors.Is(err, errInvalidCmdline) {
		fmt.Fprint(app.stderr, usageSynopsis)
		app.printFlagDefaults()
	}

	// Flush any buffered output before exiting with error
	if app.formatter != nil {
		_ = app.formatter.Flush()
	}

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
