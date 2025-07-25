// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// dutagent is the server of the DUT Control system.
// The service ist designed to run on a single board computer,
// which can handle the wiring to the devices under test (DUTs).
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
	"os/signal"
	"syscall"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/buildinfo"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
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

	// state
	config
}

// config holds the dutagent configuration that is parsed from YAML data.
type config struct {
	Version int
	Devices dut.Devlist
}

type exitCode int

const (
	exit0 exitCode = 0
	exit1 exitCode = 1
)

// cleanup takes care of a graceful shutdown of the agt and its running service.
// Afterwards agt.exit is called. If clean-up fails, agt.exit is called with code 1,
// otherwise with provided exitCode.
func (agt *agent) cleanup(code exitCode) {
	devlist := agt.config.Devices
	if devlist != nil {
		err := dutagent.Deinit(devlist)
		if err != nil {
			printInitErr(err)
			log.Print("System might be in an UNKNOWN STATE !!!")
			agt.exit(1)
		}
	}

	agt.exit(int(code))
}

// watchInterrupt listens for interrupt signals, usually triggered by the user
// terminating the process with Ctrl-C.
func (agt *agent) watchInterrupt() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	go func() {
		sig := <-c
		log.Printf("Captured signal: %v", sig)
		agt.cleanup(exit0)
	}()
}

func (agt *agent) loadConfig() error {
	cfgYAML, err := os.ReadFile(agt.configPath)
	if err != nil {
		return err
	}

	if err := yaml.Unmarshal(cfgYAML, &agt.config); err != nil {
		return fmt.Errorf("parsing YAML failed: %w", err)
	}

	return nil
}

func (agt *agent) initModules() error {
	return dutagent.Init(agt.config.Devices)
}

// printInitErr extracts and pretty-prints the details of a dutagent.ModuleInitErr
// if err is of this type, otherwise it just prints err.
func printInitErr(err error) {
	var initerr *dutagent.ModuleInitError
	if errors.As(err, &initerr) {
		for _, item := range initerr.Errs {
			devstr := fmt.Sprintf("dev:%q cmd:%q module:%q", item.Dev, item.Cmd, item.Mod.Config.Name)
			log.Printf("init %s failed with:\n%v\n", devstr, item.Err)
		}
	}

	log.Print(err)
}

// startRPCService starts the RPC service, that ideally listens for incoming
// connections forever. It always returns an non-nil error.
func (agt *agent) startRPCService() error {
	service := &rpcService{
		devices: agt.config.Devices,
	}

	mux := http.NewServeMux()
	path, handler := dutctlv1connect.NewDeviceServiceHandler(service)
	mux.Handle(path, handler)

	//nolint:gosec
	return http.ListenAndServe(
		agt.address,
		// Use h2c so we can serve HTTP/2 without TLS.
		h2c.NewHandler(mux, &http2.Server{}),
	)
}

func (agt *agent) registerWithServer() error {
	log.Printf("Registering with server %q", agt.server)

	client := spawnClient(agt.server)
	req := connect.NewRequest(&pb.RegisterRequest{
		Devices: agt.config.Devices.Names(),
		Address: agt.address,
	})

	_, err := client.Register(context.Background(), req)
	if err != nil {
		return fmt.Errorf("registering with server %q failed: %w", agt.server, err)
	}

	log.Printf("Successfully registered with server %q", agt.server)

	return nil
}

// spawnClient creates a new client to the DUT server specified by the server address.
// TODO: refactor into pkg and reuse in dutctl and dutserver.
//
//nolint:ireturn
func spawnClient(agendURL string) dutctlv1connect.RelayServiceClient {
	log.Printf("Spawning new client for agent %q", agendURL)

	return dutctlv1connect.NewRelayServiceClient(
		// Instead of http.DefaultClient, use the HTTP/2 protocol without TLS
		newInsecureClient(),
		fmt.Sprintf("http://%s", agendURL),
		connect.WithGRPC(),
	)
}

// TODO: refactor into pkg and reuse in dutctl and dutserver.
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
			// TODO: Don't forget timeouts!
		},
	}
}

// start orchestrates the dutagent execution.
//
//nolint:cyclop
func (agt *agent) start() {
	log.SetOutput(agt.stdout)

	if agt.versionFlag {
		agt.printVersion()
		agt.exit(0)
	}

	// By design dutagent's code does not panic.
	// But other code could, or *things* happen at runtime. So we catch it here
	// to do a graceful shutdown
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic: %v", r)
			agt.cleanup(exit1)
		}
	}()

	agt.watchInterrupt()

	err := agt.loadConfig()
	if agt.checkConfig {
		if err != nil {
			log.Printf("Bad configuration: %v", err)
			agt.cleanup(exit0)
		}

		log.Print("Configuration is valid")
		agt.cleanup(exit0)
	} else if err != nil {
		log.Printf("Loading config failed: %v", err)
		agt.cleanup(exit1)
	}

	err = agt.initModules()
	if agt.dryRun {
		if err != nil {
			printInitErr(err)
			log.Print("Initialization FAILED - Dry run finished")
			agt.cleanup(exit0)
		}

		log.Print("Initialization SUCCESSFUL - Dry run finished")
		agt.cleanup(exit0)
	} else if err != nil {
		printInitErr(err)
		agt.cleanup(exit1)
	}

	if agt.server != "" {
		if err := agt.registerWithServer(); err != nil {
			log.Printf("Registering with server %q failed: %v", agt.server, err)
			agt.cleanup(exit1)
		}
	}

	err = agt.startRPCService()
	log.Printf("internal RPC handler error: %v", err)
	agt.cleanup(exit1)
}

func (agt *agent) printVersion() {
	fmt.Fprint(agt.stdout, "DUT Control Agent\n")
	fmt.Fprint(agt.stdout, buildinfo.VersionString())
}

func main() {
	newAgent(os.Stdout, os.Exit, os.Args).start()
}
