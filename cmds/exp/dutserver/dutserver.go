// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// dutserver is EXPERIMENTAL! It serves as a relay for dutctl requests to
// multiple registered DUT agents.
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
)

const (
	addressInfo  = `Server address and port in the format: address:port`
	logLevelInfo = `Log level: debug, info, warn, or error`
	logJSONInfo  = `Emit logs as JSON instead of human-readable text`
)

func newServer(exitFunc func(int), args []string) *server {
	var svr server

	svr.exit = exitFunc

	f := flag.NewFlagSet(args[0], flag.ExitOnError)
	f.StringVar(&svr.address, "s", "localhost:1024", addressInfo)
	f.StringVar(&svr.logLevel, "log", "debug", logLevelInfo)
	f.BoolVar(&svr.logJSON, "log-json", false, logJSONInfo)

	//nolint:errcheck // flag.Parse never returns an error because of flag.ExitOnError
	f.Parse(args[1:])

	return &svr
}

// server represents the dutserver application.
type server struct {
	exit func(int)

	// flags
	address  string
	logLevel string
	logJSON  bool
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
		slog.Info("captured signal", "signal", sig)
		svr.cleanup(exit0)
	}()
}

// readHeaderTimeout bounds how long the server waits to read request headers.
const readHeaderTimeout = 10 * time.Second

// startRPCService starts the RPC service, that ideally listens for incoming
// connections forever. It always returns an non-nil error.
func (svr *server) startRPCService() error {
	// TODO: load registered DUTs from a file.
	service := &rpcService{
		agents: make(map[string]*agent),
	}

	mux := http.NewServeMux()
	// Register the RPC service handler used by the dutctl client to
	// communicate with the server. dutserver relays the version headers between
	// client and agent (see rpcService.Run).
	path, handler := dutctlv1connect.NewDeviceServiceHandler(service)
	mux.Handle(path, handler)
	// Register the RPC service handler used by dut agents to register themselves
	// and their devices with the server.
	path, handler = dutctlv1connect.NewRelayServiceHandler(service)
	mux.Handle(path, handler)

	// Serve HTTP/2 without TLS (h2c)
	srv := &http.Server{
		Addr:              svr.address,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)

	slog.Info("rpc service listening", "addr", svr.address)

	return srv.ListenAndServe()
}

// start orchestrates the dutserver execution.
func (svr *server) start() {
	// Install the process-wide structured logger. Service diagnostics go to
	// stderr; the default scope is "server" and request handlers replace the
	// scope as control enters their subsystem. See package internal/log.
	base := log.New(os.Stderr, log.ParseLevel(svr.logLevel), svr.logJSON)
	slog.SetDefault(log.Scope(base, "server"))

	// By design dutserver's code does not panic.
	// But other code could, or *things* happen at runtime. So we catch it here
	// to do a graceful shutdown
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered from panic", "panic", r)
			svr.cleanup(exit1)
		}
	}()

	svr.watchInterrupt()

	// TODO: Load registered agents and their list of DUTs from a file.
	// - Handle name conflicts, e.g., if the same device name is present on multiple registered agents.
	// - The device names over all registered agents should be unique in the for they are maintained in the server.

	err := svr.startRPCService() // runs forever

	slog.Error("rpc service stopped", "err", err)
	svr.cleanup(exit1)
}

func main() {
	newServer(os.Exit, os.Args).start()
}
