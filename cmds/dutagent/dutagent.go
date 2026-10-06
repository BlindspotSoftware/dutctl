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
	"runtime/debug"
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
	addressInfo = `Address to run the agent on in the format: address[:port], the port defaults to ` + rpc.DefaultPort +
		`; an empty address as in :port means all interfaces`
	configPathInfo  = `Path to DUT configuration file`
	checkConfigInfo = `Only validate the provided DUT configuration, not starting the service`
	dryRunInfo      = `Only run the initialization phase of the modules, not start the (includes validation of the configuration)`
	serverInfo      = `Optional DUT Server to register with in the format: address[:port], the port defaults to ` + rpc.DefaultPort
	versionFlagInfo = `Print version information and exit`
	logLevelInfo    = `Log level: debug, info, warn, or error`
	logJSONInfo     = `Emit logs as JSON instead of human-readable text`
)

func newAgent(stdout io.Writer, exitFunc func(int), args []string) *agent {
	var agt agent

	agt.stdout = stdout
	agt.exit = exitFunc

	fs := flag.NewFlagSet(args[0], flag.ExitOnError)
	fs.StringVar(&agt.address, "a", "localhost:"+rpc.DefaultPort, addressInfo)
	fs.StringVar(&agt.configPath, "c", "dutctl.yaml", configPathInfo)
	fs.BoolVar(&agt.checkConfig, "check-config", false, checkConfigInfo)
	fs.BoolVar(&agt.dryRun, "dry-run", false, dryRunInfo)
	fs.StringVar(&agt.server, "server", "", serverInfo)
	fs.BoolVar(&agt.versionFlag, "v", false, versionFlagInfo)
	fs.StringVar(&agt.logLevel, "log", "info", logLevelInfo)
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
	locks             *locker.Locker
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
// unreachable or slow server fails fast instead of hanging agent startup. The
// first stop signal ends the registration too.
const registerTimeout = 10 * time.Second

// deinitTimeout bounds module de-initialization during shutdown so a wedged module
// cannot hang teardown indefinitely.
const deinitTimeout = 15 * time.Second

// initTimeout ends the context of every module's Init 5 min after the modules
// began to initialize; a module that honors it, say one probing absent
// hardware, then fails the startup instead of hanging it. It is generous
// because Init may legitimately talk to slow devices.
const initTimeout = 5 * time.Minute

// cleanup deinitializes the modules, if they were initialized, and then calls
// agt.exit. If clean-up fails, agt.exit is called with code 1, otherwise with
// the provided exitCode. The caller makes sure that no command runs any more,
// so no module is deinitialized while it runs. ctx is the agent's lifetime
// context, which carries its logger.
func (agt *agent) cleanup(ctx context.Context, code exitCode) {
	if agt.modulesNeedDeinit {
		// Bound Deinit: deinitWithin leaves behind a module that ignores its context.
		ctx, cancel := context.WithTimeout(ctx, deinitTimeout)
		defer cancel()

		err := deinitWithin(ctx, agt.config.Devices)
		if err != nil {
			printInitErr(err)
			slog.Error("module deinitialization failed - system might be in an UNKNOWN STATE", "err", err)
			agt.exit(1)
		}
	}

	agt.exit(int(code))
}

// completeAddrs gives the agent's own address and the dutserver's
// rpc.DefaultPort where they name no port. The agent does so at the start,
// before the modules take their time to initialize, so a malformed address
// fails at once, and so it registers the address it listens on.
func (agt *agent) completeAddrs() error {
	addr, err := rpc.ListenAddr(agt.address)
	if err != nil {
		return fmt.Errorf("-a: %w", err)
	}

	agt.address = addr

	if agt.server == "" {
		return nil
	}

	server, err := rpc.DialAddr(agt.server)
	if err != nil {
		return fmt.Errorf("-server: %w", err)
	}

	agt.server = server

	return nil
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

// startRPCService serves service until ctx is cancelled, draining in-flight
// requests, or until the server stops on its own. It returns the server error,
// if any; the caller classifies a graceful stop via ctx.Err().
func (agt *agent) startRPCService(ctx context.Context, service *rpcService) error {
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

func (agt *agent) registerWithServer(ctx context.Context) error {
	slog.Info("registering with server", "server", agt.server)

	client := rpc.NewRelayClient(agt.server)
	req := connect.NewRequest(&pb.RegisterRequest{
		Devices: agt.config.Devices.Names(),
		Address: agt.address,
	})

	ctx, cancel := context.WithTimeout(ctx, registerTimeout)
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

	// ctx is the agent's lifetime context; it carries the agent-scoped logger.
	ctx := log.Into(context.Background(), slog.Default())

	agt.locks = locker.New()

	// Stop signals take the agent through its stop stages from here on (see
	// stopper). Until the RPC service runs, the first one interrupts the startup.
	stop, stopNotify := notifyStops(ctx, agt.locks, agt.exit)
	defer stopNotify()

	// By design dutagent's code does not panic.
	// But other code could, or *things* happen at runtime. So we catch it here
	// to do a graceful shutdown: commands may still run, so abort them as the
	// second stop stage does before the modules are deinitialized.
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered from panic", "panic", r, "stack", string(debug.Stack()))
			stop.abortAndWait(ctx)
			agt.cleanup(ctx, exit1)
		}
	}()

	err := agt.completeAddrs()
	if err != nil {
		slog.Error("invalid address", "err", err)
		agt.cleanup(ctx, exit1)
	}

	err = agt.loadConfig()
	if agt.checkConfig {
		if err != nil {
			slog.Error("bad configuration", "err", err)
			agt.cleanup(ctx, exit1)
		}

		slog.Info("configuration is valid")
		agt.cleanup(ctx, exit0)
	} else if err != nil {
		slog.Error("loading config failed", "err", err)
		agt.cleanup(ctx, exit1)
	}

	// initCtx is the parent of every module's Init context. It ends with the first
	// stop signal, so Ctrl-C interrupts a slow startup, and after initTimeout, so
	// an Init that honors its context cannot block the startup forever.
	initCtx, cancelInit := context.WithTimeout(stop.draining, initTimeout)
	defer cancelInit()

	agt.modulesNeedDeinit = true
	err = initModules(initCtx, agt.config.Devices)

	// A stop signal during the startup ends it here, also when a module's Init
	// ignored its context; an Init error it caused is no failure of its own.
	if stop.draining.Err() != nil {
		slog.Info("stopped during startup")
		agt.cleanup(ctx, exit0)
	}

	if agt.dryRun {
		if err != nil {
			printInitErr(err)
			slog.Info("initialization failed - dry run finished")
			agt.cleanup(ctx, exit1)
		}

		slog.Info("initialization successful - dry run finished")
		agt.cleanup(ctx, exit0)
	} else if err != nil {
		printInitErr(err)
		slog.Error("module initialization failed", "err", err)
		agt.cleanup(ctx, exit1)
	}

	if agt.server != "" {
		// A stop signal may also arrive while the agent registers.
		err := agt.registerWithServer(stop.draining)
		if stop.draining.Err() != nil {
			slog.Info("stopped during startup")
			agt.cleanup(ctx, exit0)
		}

		if err != nil {
			slog.Error("registering with server failed", "server", agt.server, "err", err)
			agt.cleanup(ctx, exit1)
		}
	}

	agt.cleanup(ctx, agt.serve(ctx, stop))
}

func (agt *agent) printVersion() {
	fmt.Fprint(agt.stdout, "DUT Control Agent\n")
	fmt.Fprint(agt.stdout, buildinfo.VersionString())
}

func main() {
	newAgent(os.Stdout, os.Exit, os.Args).start()
}
