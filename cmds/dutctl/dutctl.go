// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// dutctl is the client application of the DUT Control system.
// It provides a command line interface to issue task on remote devices (DUTs).
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"

	"connectrpc.com/connect"
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

const serverInfo = `Address and port of the dutagent to connect to in the format: address:port`

func newApp(stdin io.Reader, stdout, stderr io.Writer, args []string) *application {
	var app application

	app.stdout = stdout
	app.stderr = stderr
	app.stdin = stdin

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
	fs.StringVar(&app.serverAddr, "s", "localhost:1024", serverInfo)

	//nolint:errcheck // flag.Parse always returns no error because of flag.ExitOnError
	fs.Parse(args[1:])
	app.args = fs.Args()

	return &app
}

type application struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	// flags
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

var ErrInvalidArgs = fmt.Errorf("invalid command-line arguments")

// start is the entry point of the application.
func (app *application) start() error {
	log.SetOutput(app.stdout)

	app.setupRPCClient()

	if len(app.args) == 0 {
		fmt.Fprint(app.stderr, usageSynopsis)

		return ErrInvalidArgs
	}

	if app.args[0] == "list" {
		if len(app.args) > 1 {
			fmt.Fprint(app.stderr, usageSynopsis)
			app.printFlagDefaults()

			return ErrInvalidArgs
		}

		fmt.Fprintf(app.stdout, "Calling List-RPC\non dutagent %s\n",
			app.serverAddr)

		return app.listRPC()
	}

	if len(app.args) == 1 {
		device := app.args[0]
		fmt.Fprintf(app.stdout, "Calling Commands-RPC with\ndevice=%q\non dutagent %s\n",
			device, app.serverAddr)

		return app.commandsRPC(device)
	}

	device := app.args[0]
	command := app.args[1]
	cmdArgs := app.args[2:]

	if len(cmdArgs) > 0 && cmdArgs[0] == "help" {
		fmt.Fprintf(app.stdout, "Calling Details-RPC with\ndevice=%q\ncommand=%q\nkeyword=%q\non dutagent %s\n",
			device, command, "help", app.serverAddr)

		return app.detailsRPC(device, command, "help")
	}

	fmt.Fprintf(app.stdout, "Calling Run-RPC with\ndevice=%q\ncommand=%q\ncmdArgs=%q\non dutagent %s\n",
		device, command, cmdArgs, app.serverAddr)

	return app.runRPC(device, command, cmdArgs)
}

func main() {
	err := newApp(os.Stdin, os.Stdout, os.Stderr, os.Args).start()
	if err != nil {
		log.Fatal(err)
	}
}
