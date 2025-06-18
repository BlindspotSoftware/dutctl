// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// dutserver is EXPERIMENTAL! It serves as a relay for dutctl requests to
// multiple registered DUT agents.
package main

import (
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const (
	addressInfo = `Server address and port in the format: address:port`
)

func newServer(stdout io.Writer, exitFunc func(int), args []string) *server {
	var svr server

	svr.stdout = stdout
	svr.exit = exitFunc

	f := flag.NewFlagSet(args[0], flag.ExitOnError)
	f.StringVar(&svr.address, "s", "localhost:1024", addressInfo)

	//nolint:errcheck // flag.Parse never returns an error because of flag.ExitOnError
	f.Parse(args[1:])

	return &svr
}

// server represents the dutserver application.
type server struct {
	stdout io.Writer
	exit   func(int)

	// flags
	address string
}

type exitCode int

const (
	exit0 exitCode = 0
	exit1 exitCode = 1
)

// cleanup takes care of a graceful shutdown of svr and its running service.
// Afterwards svr.exit is called. If clean-up fails, svr.exit is called with code 1,
// otherwise with provided exitCode.
func (svr *server) cleanup(code exitCode) {
	// TODO: save registered agents to a file, so we can restore them on next start
	svr.exit(int(code))
}

// watchInterrupt listens for interrupt signals, usually triggered by the user
// terminating the process with Ctrl-C.
func (svr *server) watchInterrupt() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	go func() {
		sig := <-c
		log.Printf("Captured signal: %v", sig)
		svr.cleanup(exit0)
	}()
}

// startRPCService starts the RPC service, that ideally listens for incoming
// connections forever. It always returns an non-nil error.
func (svr *server) startRPCService() error {
	// TODO: populate agents map with registered DUT agents.
	// For now, it is hardcoded test data.
	service := &rpcService{
		agents: map[string]*agent{
			"device1": {
				address: "localhost:1025",
			},
			"device2": {
				address: "localhost:1026",
			},
		},
	}

	mux := http.NewServeMux()
	path, handler := dutctlv1connect.NewDeviceServiceHandler(service)
	mux.Handle(path, handler)

	//nolint:gosec
	return http.ListenAndServe(
		svr.address,
		// Use h2c so we can serve HTTP/2 without TLS.
		h2c.NewHandler(mux, &http2.Server{}),
	)
}

// start orchestrates the dutagent execution.
func (svr *server) start() {
	log.SetOutput(svr.stdout)

	// By design dutserver's code does not panic.
	// But other code could, or *things* happen at runtime. So we catch it here
	// to do a graceful shutdown
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic: %v", r)
			svr.cleanup(exit1)
		}
	}()

	svr.watchInterrupt()

	// TODO: Load registered agents and their list of DUTs from a file.
	// - Handle name conflicts, e.g., if the same device name is present on multiple registered agents.
	// - The device names over all registered agents should be unique in the for they are maintained in the server.

	err := svr.startRPCService() // runs forever

	log.Printf("internal RPC handler error: %v", err)
	svr.cleanup(exit1)
}

func main() {
	newServer(os.Stdout, os.Exit, os.Args).start()
}
