// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// dutctl is the client application of the DUT Control system.
// It provides a command line interface to issue task on remote devices (DUTs).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"

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
	fs.StringVar(&app.serverAddr, "s", "localhost:1024", serverAddrInfo)
	fs.StringVar(&app.outputFormat, "f", "", outputFormatInfo)
	fs.BoolVar(&app.verbose, "v", false, verboseInfo)
	fs.BoolVar(&app.noColor, "no-color", false, noColorInfo)

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
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				// If you're also using this client for non-h2c traffic, you may want
				// to delegate to tls.Dial if the network isn't TCP or the addr isn't
				// in an allowlist.
				dialer := &net.Dialer{}

				return dialer.DialContext(ctx, network, addr)
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

	if len(cmdArgs) > 0 && cmdArgs[0] == "help" {
		err := app.detailsRPC(device, command, "help")
		app.exit(err)
	}

	err := app.runRPC(device, command, cmdArgs)
	app.exit(err)
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
