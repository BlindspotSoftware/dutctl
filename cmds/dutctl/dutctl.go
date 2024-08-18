// dutctl is the client application of the DUT Control system.
// It provides a command line interface to issue task on remote devices (DUTs).

package main

import (
	"bufio"
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
	"sync"

	"connectrpc.com/connect"
	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
	"golang.org/x/net/http2"
)

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

type application struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	args []string

	client dutctlv1connect.DeviceServiceClient
}

func (app *application) listRPC() error {
	log.Println("Calling List RPC")

	ctx := context.Background()
	req := connect.NewRequest(&pb.ListRequest{})

	res, err := app.client.List(ctx, req)
	if err != nil {
		return err
	}

	fmt.Fprintln(app.stdout, res.Msg.GetDevices())

	return nil
}

func (app *application) commandsRPC(device string) error {
	log.Println("Calling Commands RPC for device2")

	ctx := context.Background()
	req := connect.NewRequest(&pb.CommandsRequest{Device: device})

	res, err := app.client.Commands(ctx, req)
	if err != nil {
		return err
	}

	fmt.Fprintln(app.stdout, res.Msg.GetCommands())

	return nil
}

//nolint:funlen,cyclop
func (app *application) runRPC(device, command string, cmdArgs []string) error {
	log.Println("Calling Run RPC ")

	wg := sync.WaitGroup{}

	ctx := context.Background()
	stream := app.client.Run(ctx)
	req := &pb.RunRequest{
		Msg: &pb.RunRequest_Command{
			Command: &pb.Command{
				Device: device,
				Cmd:    command,
				Args:   cmdArgs,
			},
		},
	}

	err := stream.Send(req)
	if err != nil {
		return err
	}

	// Receive routine
	wg.Add(1)

	go func() {
		defer wg.Done()

		for {
			res, err := stream.Receive()
			if errors.Is(err, io.EOF) {
				log.Println("Receive routine terminating: Stream closed by agent")

				return
			} else if err != nil {
				log.Fatalln(err)
			}

			//nolint:protogetter
			switch msg := res.Msg.(type) {
			case *pb.RunResponse_Print:
				fmt.Fprintln(app.stdout, string(msg.Print.GetText()))
			case *pb.RunResponse_Console:
				switch consoleData := msg.Console.Data.(type) {
				case *pb.Console_Stdout:
					fmt.Fprint(app.stdout, string(consoleData.Stdout))
				case *pb.Console_Stderr:
					fmt.Fprint(app.stdout, string(consoleData.Stderr))
				case *pb.Console_Stdin:
					log.Printf("Unexpected Console Stdin %q", string(consoleData.Stdin))
				}
			case *pb.RunResponse_FileRequest:
				log.Printf("File request for: %q\n", msg.FileRequest.GetPath())

				err := stream.Send(&pb.RunRequest{
					Msg: &pb.RunRequest_File{
						File: &pb.File{
							Path:    "in-mem",
							Content: []byte("some file content"),
						},
					},
				})

				if err != nil {
					log.Fatalln(err)
				}
			default:
				log.Printf("Unexpected message type %T", msg)
			}
		}
	}()

	// Send routine
	// No wg.Add(1) as this routine blocks on reading input, so waiting on this routine
	// is a deadlock. It will be killed, when the applications exits.
	//
	// No clue how to signal the send routine to stop, as it will block on the reader.
	// Maybe set the source of the reader to nil to unblock and check some condition / done-channel?
	go func() {
		reader := bufio.NewReader(app.stdin)

		for {
			text, err := reader.ReadString('\n')
			if err != nil {
				log.Fatalln(err)
			}

			err = stream.Send(&pb.RunRequest{
				Msg: &pb.RunRequest_Console{
					Console: &pb.Console{
						Data: &pb.Console_Stdin{
							Stdin: []byte(text),
						},
					},
				},
			})
			if err != nil {
				log.Fatalln(err)
			}
		}
	}()

	wg.Wait()

	return nil
}

func start(stdin io.Reader, stdout, stderr io.Writer, args []string) {
	app := application{
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}

	fs := flag.NewFlagSet(args[0], flag.ExitOnError)
	//nolint:errcheck
	fs.Parse(args[1:])

	app.args = flag.Args()

	client := dutctlv1connect.NewDeviceServiceClient(
		// Instead of http.DefaultClient, use the HTTP/2 protocol without TLS
		newInsecureClient(),
		"http://localhost:8080",
		connect.WithGRPC(),
	)

	app.client = client

	// ###### DEMO LIST ######
	err := app.listRPC()
	if err != nil {
		log.Fatal(err)
	}

	// ###### DEMO CMDS ######
	err = app.commandsRPC("device2")
	if err != nil {
		log.Fatal(err)
	}

	// ###### DEMO RUN (status command, expecting prints only) ######
	// err = app.runRPC("device1", "status", []string{"foo", "bar"})
	// if err != nil {
	// 	log.Fatal(err)
	// }

	// ###### DEMO RUN (repeat command, expecting interactive console messages) ######
	// err = app.runRPC("device2", "repeat", []string{})
	// if err != nil {
	// 	log.Fatal(err)
	// }

	err = app.runRPC("device3", "file-transfer", []string{"file.txt"})
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	start(os.Stdin, os.Stdout, os.Stderr, os.Args)
}
