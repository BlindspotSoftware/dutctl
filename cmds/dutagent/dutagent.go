// dutagent is the server of the DUT Control system.
// The service ist designed to run on a single board computer,
// which can handle the wiring to the devices under test (DUTs).
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gopkg.in/yaml.v3"

	_ "github.com/BlindspotSoftware/dutctl/pkg/module/dummy"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

type dutagentService struct {
	devices dut.Devlist
}

// List is the handler for the List RPC.
func (a *dutagentService) List(
	_ context.Context,
	_ *connect.Request[pb.ListRequest],
) (*connect.Response[pb.ListResponse], error) {
	log.Println("Server received List request")

	res := connect.NewResponse(&pb.ListResponse{
		Devices: a.devices.Names(),
	})

	log.Print("List-RPC finished")

	return res, nil
}

// Commands is the handler for the Commands RPC.
func (a *dutagentService) Commands(
	_ context.Context,
	req *connect.Request[pb.CommandsRequest],
) (*connect.Response[pb.CommandsResponse], error) {
	log.Println("Server received Commands request")

	device := req.Msg.GetDevice()

	res := connect.NewResponse(&pb.CommandsResponse{
		Commands: a.devices.Cmds(device),
	})

	log.Print("Commands-RPC finished")

	return res, nil
}

type config struct {
	Version int
	Devices dut.Devlist
}

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

// cleanup takes care of a graceful shutdown of the service and calls os.Exit
// afterwards. If clean-up fails, os.Exit is called with code 1, otherwise
// os.Exit is called with exitcode.
func cleanup(devlist dut.Devlist, exitcode int) {
	if devlist != nil {
		err := dutagent.Deinit(devlist)
		if err != nil {
			printInitErr(err)
			log.Fatal("System might be in an UNKNOWN STATE !!!")
		}

		os.Exit(1)
	}

	os.Exit(exitcode)
}

func main() {
	var cfg config

	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic: %v", r)
			cleanup(cfg.Devices, 1)
		}
	}()

	cfgYAML, err := os.ReadFile("./contrib/dutagent-cfg-example.yaml")
	if err != nil {
		//nolint:gocritic
		log.Fatal(err)
	}

	if err := yaml.Unmarshal(cfgYAML, &cfg); err != nil {
		log.Fatal(err)
	}

	if err = dutagent.Init(cfg.Devices); err != nil {
		printInitErr(err)
		cleanup(cfg.Devices, 1)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	go func() {
		sig := <-c
		log.Printf("Captured signal: %v", sig)
		cleanup(cfg.Devices, 0)
	}()

	agent := &dutagentService{
		devices: cfg.Devices,
	}

	mux := http.NewServeMux()
	path, handler := dutctlv1connect.NewDeviceServiceHandler(agent)
	mux.Handle(path, handler)
	//nolint:gosec
	err = http.ListenAndServe(
		"localhost:8080",
		// Use h2c so we can serve HTTP/2 without TLS.
		h2c.NewHandler(mux, &http2.Server{}),
	)

	log.Printf("internal RPC handler error: %v", err)
	cleanup(cfg.Devices, 1)
}
