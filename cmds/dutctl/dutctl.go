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

const usage = `dutctl is the client application of the DUT Control system.

TODO: Add synopsis here.
`

const serverInfo = `Address and port of the dutagent to connect to in the format: address:port`

func newApp(stdin io.Reader, stdout, stderr io.Writer, args []string) *application {
	var app application

	app.stdout = stdout
	app.stderr = stderr
	app.stdin = stdin

	f := flag.NewFlagSet(args[0], flag.ExitOnError)
	f.StringVar(&app.serverAddr, "s", "localhost:1024", serverInfo)
	//nolint:errcheck // flag.Parse always returns no error because of flag.ExitOnError
	f.Parse(args[1:])

	app.args = f.Args()

	return &app
}

type application struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	// flags
	serverAddr string
	args       []string

	rpcClient dutctlv1connect.DeviceServiceClient
}

func (app *application) setupRPCClient() {
	client := dutctlv1connect.NewDeviceServiceClient(
		// Instead of http.DefaultClient, use the HTTP/2 protocol without TLS
		newInsecureClient(),
		fmt.Sprintf("http://%s", app.serverAddr),
		connect.WithGRPC(),
	)

	fmt.Fprintf(app.stdout, "Connect to dutagent %s\n", app.serverAddr)

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
		fmt.Fprint(app.stderr, usage)

		return ErrInvalidArgs
	}

	if app.args[0] == "list" {
		if len(app.args) > 1 {
			fmt.Fprint(app.stderr, usage)

			return ErrInvalidArgs
		}

		fmt.Fprintf(app.stdout, "Calling List-RPC\n")

		return app.listRPC()
	}

	if len(app.args) == 1 {
		device := app.args[0]
		fmt.Fprintf(app.stdout, "Calling Commands-RPC with\ndevice=%q\n", device)

		return app.commandsRPC(device)
	}

	device := app.args[0]
	command := app.args[1]
	cmdArgs := app.args[2:]

	if len(cmdArgs) > 0 && cmdArgs[0] == "help" {
		fmt.Fprintf(app.stdout, "Calling Details-RPC with\ndevice=%q\ncommand=%q\nkeyword=%q\n", device, command, "help")

		return app.detailsRPC(device, command, "help")
	}

	fmt.Fprintf(app.stdout, "Calling Run-RPC with\ndevice=%q\ncommand=%q\ncmdArgs=%q\n", device, command, cmdArgs)

	return app.runRPC(device, command, cmdArgs)
}

func main() {
	err := newApp(os.Stdin, os.Stdout, os.Stderr, os.Args).start()
	if err != nil {
		log.Fatal(err)
	}
}
