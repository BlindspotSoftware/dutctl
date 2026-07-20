// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// dutagent is the server of the DUT Control system.
// The service is designed to run on a single board computer,
// which can handle the wiring to the devices under test (DUTs).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/buildinfo"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/locker"
	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/internal/rpc"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
	"gopkg.in/yaml.v3"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

const (
	addressInfo     = `Address and port to run the agent in the format: address:port`
	configPathInfo  = `Path to DUT configuration file`
	checkConfigInfo = `Only validate the provided DUT configuration, not starting the service`
	dryRunInfo      = `Only run the initialization phase of the modules, not start the (includes validation of the configuration)`
	serverInfo      = `Optional DUT Server address and port to register with in the format: address:port`
	versionFlagInfo = `Print version information and exit`
	logLevelInfo    = `Log level: debug, info, warn, or error`
	logJSONInfo     = `Emit logs as JSON instead of human-readable text`
)

func newAgent(stdout io.Writer, exitFunc func(int), args []string) *agent {
	var agt agent

	agt.stdout = stdout
	agt.exit = exitFunc

	fs := flag.NewFlagSet(args[0], flag.ExitOnError)
	fs.StringVar(&agt.address, "a", "localhost:1024", addressInfo)
	fs.StringVar(&agt.configPath, "c", "dutctl.yaml", configPathInfo)
	fs.BoolVar(&agt.checkConfig, "check-config", false, checkConfigInfo)
	fs.BoolVar(&agt.dryRun, "dry-run", false, dryRunInfo)
	fs.StringVar(&agt.server, "server", "", serverInfo)
	fs.BoolVar(&agt.versionFlag, "v", false, versionFlagInfo)
	fs.StringVar(&agt.logLevel, "log", "debug", logLevelInfo)
	fs.BoolVar(&agt.logJSON, "log-json", false, logJSONInfo)
	//nolint:errcheck // flag.Parse always returns no error because of flag.ExitOnError
	fs.Parse(args[1:])

	return &agt
}

// agent represents the dutagent application.
type agent struct {
	stdout io.Writer
	exit   func(int)

	// flags
	versionFlag bool
	address     string
	configPath  string
	checkConfig bool
	dryRun      bool
	server      string
	logLevel    string
	logJSON     bool

	// state
	config            config
	modulesNeedDeinit bool
}

// config holds the dutagent configuration that is parsed from YAML data.
type config struct {
	Version string
	Devices dut.Devlist
}

type exitCode int

const (
	exit0 exitCode = 0
	exit1 exitCode = 1
)

// registerTimeout bounds the one-shot registration RPC to the dutserver. Connect
// propagates it as a grpc-timeout header and the transport honors it, so an
// unreachable or slow server fails fast instead of hanging agent startup.
const registerTimeout = 10 * time.Second

// deinitTimeout bounds module de-initialization during shutdown so a wedged module
// cannot hang teardown indefinitely.
const deinitTimeout = 15 * time.Second

// cleanup takes care of a graceful shutdown of the agent and its running service.
// Afterwards agt.exit is called. If clean-up fails, agt.exit is called with code 1,
// otherwise with the provided exitCode.
func (agt *agent) cleanup(code exitCode) {
	if agt.modulesNeedDeinit {
		// Bound Deinit so a wedged module cannot hang shutdown indefinitely; the
		// context flows into every module's Deinit via internal/log.
		ctx, cancel := context.WithTimeout(context.Background(), deinitTimeout)
		defer cancel()

		err := deinitModules(ctx, agt.config.Devices)
		if err != nil {
			printInitErr(err)
			slog.Error("module deinitialization failed - system might be in an UNKNOWN STATE", "err", err)
			agt.exit(1)
		}
	}

	agt.exit(int(code))
}

func (agt *agent) loadConfig() error {
	slog.Info("loading configuration", "path", agt.configPath)

	cfgYAML, err := os.ReadFile(agt.configPath)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(cfgYAML, &agt.config)
	if err != nil {
		return fmt.Errorf("parsing YAML failed: %w", err)
	}

	return nil
}

// printInitErr extracts and pretty-prints the details of a moduleInitError
// if err is of this type, otherwise it just prints err.
func printInitErr(err error) {
	var initerr *moduleInitError
	if errors.As(err, &initerr) {
		// Phase-agnostic detail dump; the caller logs the phase-labeled summary
		// ("module initialization/deinitialization failed").
		for _, item := range initerr.Errs {
			slog.Error("module error",
				"device", item.Dev, "command", item.Cmd, "module", item.Mod.Config.Name, "err", item.Err)
		}

		return
	}

	slog.Error("module error", "err", err)
}

// startRPCService starts the RPC service and serves until ctx is cancelled (a
// signal), draining in-flight requests, or until the server stops on its own. It
// returns the server error, if any; the caller classifies a graceful stop via
// ctx.Err().
func (agt *agent) startRPCService(ctx context.Context) error {
	service := &rpcService{
		devices: agt.config.Devices,
		locker:  locker.New(),
	}

	mux := http.NewServeMux()
	path, handler := dutctlv1connect.NewDeviceServiceHandler(
		service,
		connect.WithInterceptors(
			rpc.NewVersionEnforcer(buildinfo.Version),
			rpc.NewIdentifier(),
		),
	)
	mux.Handle(path, handler)

	slog.Info("rpc service listening", "addr", agt.address)

	return rpc.ListenAndServe(ctx, agt.address, mux)
}

func (agt *agent) registerWithServer() error {
	slog.Info("registering with server", "server", agt.server)

	client := rpc.NewRelayClient(agt.server)
	req := connect.NewRequest(&pb.RegisterRequest{
		Devices: agt.config.Devices.Names(),
		Address: agt.address,
	})

	ctx, cancel := context.WithTimeout(context.Background(), registerTimeout)
	defer cancel()

	_, err := client.Register(ctx, req)
	if err != nil {
		return fmt.Errorf("registering with server %q failed: %w", agt.server, err)
	}

	slog.Info("successfully registered with server", "server", agt.server)

	return nil
}

// start orchestrates the dutagent execution.
//
//nolint:cyclop,funlen // top-level orchestration: inherently branchy and sequential
func (agt *agent) start() {
	// Install the process-wide structured logger. Service diagnostics go to
	// stderr (stdout is reserved for program output such as the version banner).
	// The default is scoped "agent"; request handlers replace the scope as
	// control enters their subsystem. See package internal/log.
	base := log.New(os.Stderr, log.ParseLevel(agt.logLevel), agt.logJSON)
	slog.SetDefault(log.Scope(base, "agent"))

	if agt.versionFlag {
		agt.printVersion()
		agt.exit(0)
	}

	// By design dutagent's code does not panic.
	// But other code could, or *things* happen at runtime. So we catch it here
	// to do a graceful shutdown
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered from panic", "panic", r, "stack", string(debug.Stack()))
			agt.cleanup(exit1)
		}
	}()

	// A signal (Ctrl-C / SIGTERM / SIGQUIT) cancels ctx, which drives a graceful
	// shutdown: the RPC service drains in-flight requests, then modules are
	// de-initialised. This replaces an out-of-band signal handler, so shutdown runs
	// on this goroutine rather than racing the running service.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	defer stop()

	err := agt.loadConfig()
	if agt.checkConfig {
		if err != nil {
			slog.Error("bad configuration", "err", err)
			agt.cleanup(exit0)
		}

		slog.Info("configuration is valid")
		agt.cleanup(exit0)
	} else if err != nil {
		slog.Error("loading config failed", "err", err)
		agt.cleanup(exit1)
	}

	// initCtx is the agent-lifetime context for module initialization; it flows
	// into every module's Init via internal/log.
	// TODO(ctx): bound startup with a timeout or wire in cancellation.
	initCtx := context.Background()

	agt.modulesNeedDeinit = true
	err = initModules(initCtx, agt.config.Devices)

	if agt.dryRun {
		if err != nil {
			printInitErr(err)
			slog.Info("initialization failed - dry run finished")
			agt.cleanup(exit0)
		}

		slog.Info("initialization successful - dry run finished")
		agt.cleanup(exit0)
	} else if err != nil {
		printInitErr(err)
		slog.Error("module initialization failed", "err", err)
		agt.cleanup(exit1)
	}

	if agt.server != "" {
		err := agt.registerWithServer()
		if err != nil {
			slog.Error("registering with server failed", "server", agt.server, "err", err)
			agt.cleanup(exit1)
		}
	}

	err = agt.startRPCService(ctx)
	if ctx.Err() != nil {
		// A signal cancelled ctx: graceful shutdown. ListenAndServe has drained; a
		// non-nil err means the drain did not fully complete within the grace
		// period, which we accept — the process exit closes what remains.
		if err != nil {
			slog.Warn("graceful shutdown did not fully drain in time", "err", err)
		}

		slog.Info("shutting down")
		agt.cleanup(exit0)
	}

	// Reached only if the server stopped on its own (e.g. failed to bind).
	slog.Error("rpc service stopped", "err", err)
	agt.cleanup(exit1)
}

func (agt *agent) printVersion() {
	fmt.Fprint(agt.stdout, "DUT Control Agent\n")
	fmt.Fprint(agt.stdout, buildinfo.VersionString())
}

func main() {
	newAgent(os.Stdout, os.Exit, os.Args).start()
}
