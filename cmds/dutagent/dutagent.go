// dutagent is the server of the DUT Control system.
// The service ist designed to run on a single board computer,
// which can handle the wiring to the devices under test (DUTs).
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gopkg.in/yaml.v3"

	_ "github.com/BlindspotSoftware/dutctl/pkg/module/dummy"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

type dutagent struct {
	devices devlist
}

// List is the handler for the List RPC.
func (a *dutagent) List(
	_ context.Context,
	_ *connect.Request[pb.ListRequest],
) (*connect.Response[pb.ListResponse], error) {
	log.Println("Server received List request")

	res := connect.NewResponse(&pb.ListResponse{
		Devices: a.devices.names(),
	})

	return res, nil
}

// Commands is the handler for the Commands RPC.
func (a *dutagent) Commands(
	_ context.Context,
	req *connect.Request[pb.CommandsRequest],
) (*connect.Response[pb.CommandsResponse], error) {
	log.Println("Server received Commands request")

	device := req.Msg.GetDevice()

	res := connect.NewResponse(&pb.CommandsResponse{
		Commands: a.devices.cmds(device),
	})

	return res, nil
}

type config struct {
	Version int
	Devices devlist
}

func main() {
	cfgYAML, err := os.ReadFile("./contrib/dutagent-cfg-example.yaml")
	if err != nil {
		log.Fatal(err)
	}

	var cfg config
	if err := yaml.Unmarshal(cfgYAML, &cfg); err != nil {
		log.Fatal(err)
	}

	agent := &dutagent{
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

	log.Fatalln(err)
}
