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

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/buildinfo"
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
	serverAddrInfo  = `Address and port of the dutagent to connect to in the format: address:port`
	versionFlagInfo = `Print version information and exit`
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
	fs.BoolVar(&app.versionFlag, "v", false, versionFlagInfo)

	//nolint:errcheck // flag.Parse always returns no error because of flag.ExitOnError
	fs.Parse(args[1:])
	app.args = fs.Args()

	return &app
}

type application struct {
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
	exitFunc func(int)

	// flags
	versionFlag       bool
	serverAddr        string
	args              []string
	printFlagDefaults func()

	rpcClient dutctlv1connect.DeviceServiceClient
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

	if app.versionFlag {
		app.printVersion()
		app.exit(nil)
	}

	app.setupRPCClient()

	if len(app.args) == 0 {
		app.exit(errInvalidCmdline)
	}

	if app.args[0] == "list" {
		if len(app.args) > 1 {
			app.exit(errInvalidCmdline)
		}

		fmt.Fprintf(app.stdout, "Calling List-RPC\non dutagent %s\n",
			app.serverAddr)
		app.printLine()

		err := app.listRPC()
		app.exit(err)
	}

	if len(app.args) == 1 {
		device := app.args[0]
		fmt.Fprintf(app.stdout, "Calling Commands-RPC with\ndevice: %q\non dutagent %s\n",
			device, app.serverAddr)
		app.printLine()

		err := app.commandsRPC(device)
		app.exit(err)
	}

	device := app.args[0]
	command := app.args[1]
	cmdArgs := app.args[2:]

	if len(cmdArgs) > 0 && cmdArgs[0] == "help" {
		fmt.Fprintf(app.stdout, "Calling Details-RPC with\ndevice: %q, command: %q, keyword: %q\non dutagent %s\n",
			device, command, "help", app.serverAddr)
		app.printLine()

		err := app.detailsRPC(device, command, "help")
		app.exit(err)
	}

	fmt.Fprintf(app.stdout, "Calling Run-RPC with\ndevice: %q, command: %q, cmdArgs: %q\non dutagent %s\n",
		device, command, cmdArgs, app.serverAddr)
	app.printLine()

	err := app.runRPC(device, command, cmdArgs)
	app.exit(err)
}

// exit terminates the application. If the provided error is not nil, it is printed to
// the standard error output. If printUsage is true, the usage information is printed additionally.
func (app *application) exit(err error) {
	if err == nil {
		app.exitFunc(0)
	}

	if err != nil {
		log.Print(err)
	}

	if errors.Is(err, errInvalidCmdline) {
		fmt.Fprint(app.stderr, usageSynopsis)
		app.printFlagDefaults()
	}

	app.exitFunc(1)
}

func (app *application) printVersion() {
	fmt.Fprint(app.stdout, "DUT Control Client\n")
	fmt.Fprint(app.stdout, buildinfo.VersionString())
}

func (app *application) printLine() {
	const length = 80

	for range length {
		fmt.Fprint(app.stdout, "-")
	}

	fmt.Fprint(app.stdout, "\n")
}

func (app *application) printList(items []string) {
	for _, i := range items {
		fmt.Fprintf(app.stdout, "- %s\n", i)
	}
}

func main() {
	newApp(os.Stdin, os.Stdout, os.Stderr, os.Exit, os.Args).start()
}
