// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// dutserver is EXPERIMENTAL! It serves as a relay for dutctl requests to
// multiple registered DUT agents.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/internal/rpc"
	"github.com/BlindspotSoftware/dutctl/internal/tlsutil"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
)

const (
	addressInfo  = `Server address and port in the format: address:port`
	logLevelInfo = `Log level: debug, info, warn, or error`
	logJSONInfo  = `Emit logs as JSON instead of human-readable text`
	insecureInfo = `Disable TLS and serve plain HTTP/2 cleartext (h2c); also used to dial the agents`
	tlsCertInfo  = `Path to the TLS certificate file; a self-signed pair is generated if neither it nor the key exists`
	tlsKeyInfo   = `Path to the TLS key file; a self-signed pair is generated if neither it nor the certificate exists`
)

// Default locations of the server's TLS key pair, mirroring dutagent's.
const (
	defaultTLSCertPath = "/var/lib/dutserver/tls/cert.pem"
	defaultTLSKeyPath  = "/var/lib/dutserver/tls/key.pem"
)

func newServer(exitFunc func(int), args []string) *server {
	var svr server

	svr.exit = exitFunc

	fs := flag.NewFlagSet(args[0], flag.ExitOnError)
	fs.StringVar(&svr.address, "s", "localhost:1024", addressInfo)
	fs.StringVar(&svr.logLevel, "log", "debug", logLevelInfo)
	fs.BoolVar(&svr.logJSON, "log-json", false, logJSONInfo)
	fs.BoolVar(&svr.insecure, "insecure", false, insecureInfo)
	fs.StringVar(&svr.tlsCertPath, "tls-cert", defaultTLSCertPath, tlsCertInfo)
	fs.StringVar(&svr.tlsKeyPath, "tls-key", defaultTLSKeyPath, tlsKeyInfo)

	//nolint:errcheck // flag.Parse never returns an error because of flag.ExitOnError
	fs.Parse(args[1:])

	return &svr
}

// server represents the dutserver application.
type server struct {
	exit func(int)

	// flags
	address     string
	logLevel    string
	logJSON     bool
	insecure    bool
	tlsCertPath string
	tlsKeyPath  string
}

// security is the transport the server serves on, and dials its agents with.
// One setting covers both directions: the relay sits between dutctl and the
// agents, and a deployment that encrypts one hop wants the other encrypted too.
func (svr *server) security() rpc.Security {
	if svr.insecure {
		return rpc.Insecure
	}

	return rpc.TLS
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

// startRPCService starts the RPC service and serves until ctx is cancelled (a
// signal), draining in-flight requests, or until the server stops on its own. It
// returns the server error, if any; the caller classifies a graceful stop via
// ctx.Err().
//
// The service is served over TLS unless -insecure was given, on the same terms
// as dutagent: the certificate comes from -tls-cert/-tls-key and is generated
// self-signed if neither exists. See internal/tlsutil for what that protects.
func (svr *server) startRPCService(ctx context.Context) error {
	// TODO: load registered DUTs from a file.
	service := &rpcService{
		agents: make(map[string]*agent),
		sec:    svr.security(),
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

	if svr.insecure {
		slog.Warn("rpc service listening WITHOUT TLS", "addr", svr.address)

		return rpc.ListenAndServe(ctx, svr.address, mux)
	}

	cert, generated, err := tlsutil.LoadOrGenerateCert(svr.tlsCertPath, svr.tlsKeyPath)
	if err != nil {
		return fmt.Errorf("loading TLS certificate: %w", err)
	}

	if generated {
		slog.Info("generated self-signed TLS certificate", "cert", svr.tlsCertPath, "key", svr.tlsKeyPath)
	}

	slog.Info("rpc service listening with TLS", "addr", svr.address, "cert", svr.tlsCertPath)

	return rpc.ListenAndServeTLS(ctx, svr.address, mux, cert)
}

// start orchestrates the dutserver execution.
func (svr *server) start() {
	// Install the process-wide structured logger. Service diagnostics go to
	// stderr; the default scope is "server" and request handlers replace the
	// scope as control enters their subsystem. See package internal/log.
	base := log.New(os.Stderr, log.ParseLevel(svr.logLevel), svr.logJSON)
	slog.SetDefault(log.Scope(base, "server"))

	// A signal (Ctrl-C / SIGTERM / SIGQUIT) cancels ctx, which drives a graceful
	// shutdown: the RPC service drains in-flight requests before returning. This
	// replaces an out-of-band signal handler, so shutdown runs on this goroutine
	// rather than racing the running service.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	defer stop()

	// TODO: Load registered agents and their list of DUTs from a file.
	// - Handle name conflicts, e.g., if the same device name is present on multiple registered agents.
	// - The device names over all registered agents should be unique in the for they are maintained in the server.

	err := svr.startRPCService(ctx)
	if ctx.Err() != nil {
		// A signal cancelled ctx: graceful shutdown. A non-nil err means the drain
		// did not fully complete within the grace period, which we accept — the
		// process exit closes what remains.
		if err != nil {
			slog.Warn("graceful shutdown did not fully drain in time", "err", err)
		}

		slog.Info("shutting down")
		svr.cleanup(exit0)
	}

	// Reached only if the server stopped on its own (e.g. failed to bind).
	slog.Error("rpc service stopped", "err", err)
	svr.cleanup(exit1)
}

func main() {
	newServer(os.Exit, os.Args).start()
}
