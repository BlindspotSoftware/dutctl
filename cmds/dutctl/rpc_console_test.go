// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
)

// runStreamServer is the agent's side of a Run stream.
type runStreamServer = *connect.BidiStream[pb.RunRequest, pb.RunResponse]

// scriptedAgent is a DeviceService whose Run is a script the test supplies; it
// records every request the client sent, in order.
type scriptedAgent struct {
	dutctlv1connect.UnimplementedDeviceServiceHandler

	run func(stream runStreamServer) error

	mu   sync.Mutex
	reqs []*pb.RunRequest
}

func (a *scriptedAgent) Run(_ context.Context, stream runStreamServer) error {
	return a.run(stream)
}

// receive takes the next request from the client and records it.
func (a *scriptedAgent) receive(stream runStreamServer) (*pb.RunRequest, error) {
	req, err := stream.Receive()
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.reqs = append(a.reqs, req)
	a.mu.Unlock()

	return req, nil
}

// inputs returns the console input the client sent, per console id, and
// the ids the client ended the input for.
func (a *scriptedAgent) inputs() (map[uint32][]byte, []uint32) {
	a.mu.Lock()
	defer a.mu.Unlock()

	data := map[uint32][]byte{}

	var ended []uint32

	for _, req := range a.reqs {
		switch msg := req.GetMsg().(type) {
		case *pb.RunRequest_ConsoleInput:
			data[msg.ConsoleInput.GetId()] = append(data[msg.ConsoleInput.GetId()], msg.ConsoleInput.GetData()...)
		case *pb.RunRequest_ConsoleControl:
			if msg.ConsoleControl.GetEof() != nil {
				ended = append(ended, msg.ConsoleControl.GetId())
			}
		}
	}

	return data, ended
}

func openRes(id uint32, mode pb.ConsoleMode) *pb.RunResponse {
	return &pb.RunResponse{Msg: &pb.RunResponse_ConsoleOpen{ConsoleOpen: &pb.ConsoleOpen{Id: id, Mode: mode}}}
}

func closeRes() *pb.RunResponse {
	return &pb.RunResponse{Msg: &pb.RunResponse_ConsoleClose{ConsoleClose: &pb.ConsoleClose{}}}
}

func stdoutRes(data []byte) *pb.RunResponse {
	return &pb.RunResponse{Msg: &pb.RunResponse_ConsoleOutput{
		ConsoleOutput: &pb.ConsoleOutput{Data: &pb.ConsoleOutput_Stdout{Stdout: data}},
	}}
}

// echoUntilEOF runs a console of the given id on the agent: it echoes the
// client's input until the client ends it.
func (a *scriptedAgent) echoUntilEOF(stream runStreamServer, id uint32, mode pb.ConsoleMode) error {
	err := stream.Send(openRes(id, mode))
	if err != nil {
		return err
	}

	for {
		req, err := a.receive(stream)
		if err != nil {
			return err
		}

		switch msg := req.GetMsg().(type) {
		case *pb.RunRequest_ConsoleInput:
			if msg.ConsoleInput.GetId() != id {
				continue // for another console: dropped, as the agent does
			}

			err = stream.Send(stdoutRes(msg.ConsoleInput.GetData()))
			if err != nil {
				return err
			}
		case *pb.RunRequest_ConsoleControl:
			if msg.ConsoleControl.GetId() == id && msg.ConsoleControl.GetEof() != nil {
				return stream.Send(closeRes())
			}
		}
	}
}

// newConsoleApp serves agent over h2c, as dutagent does, and returns an
// application wired as newApp wires it, reading stdin and writing to the
// returned buffers. The run must be started with runRPC.
func newConsoleApp(t *testing.T, agent *scriptedAgent, stdin io.Reader, format string) (*application, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	mux := http.NewServeMux()
	path, handler := dutctlv1connect.NewDeviceServiceHandler(agent)
	mux.Handle(path, handler)

	srv := httptest.NewUnstartedServer(mux)
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetHTTP1(true)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	t.Cleanup(srv.Close)

	var stdout, stderr bytes.Buffer

	args := []string{"dutctl", "-s", strings.TrimPrefix(srv.URL, "http://"), "-log", "none"}
	if format != "" {
		args = append(args, "-f", format)
	}

	app := newApp(stdin, &stdout, &stderr, func(int) {}, args)
	app.setupRPCClient()

	return app, &stdout, &stderr
}

// The client sends nothing before a console opens, marks its input with the
// console's id, forwards the final chunk of a pipe although it has no newline,
// ends the input once the pipe is at its end, and renders the echo byte for
// byte: control bytes, an escape sequence, invalid UTF-8 and the escape prefix
// itself, which a pipe does not filter.
func TestRunRPCConsoleOnPipe(t *testing.T) {
	input := "ls\r\x01x\x03\x04\x1a\x13\x1b[A\x7f\xff\xc3tail"

	agent := &scriptedAgent{}
	agent.run = func(stream runStreamServer) error {
		if _, err := agent.receive(stream); err != nil { // the command
			return err
		}

		// A Print before the console: the client must not have sent input yet.
		err := stream.Send(&pb.RunResponse{Msg: &pb.RunResponse_Print{Print: &pb.Print{Text: []byte("booting\n")}}})
		if err != nil {
			return err
		}

		return agent.echoUntilEOF(stream, 1, pb.ConsoleMode_CONSOLE_MODE_RAW)
	}

	app, stdout, _ := newConsoleApp(t, agent, strings.NewReader(input), "")

	err := app.runRPC(context.Background(), "dev", "cmd", nil)
	if err != nil {
		t.Fatalf("runRPC: %v", err)
	}

	data, ended := agent.inputs()
	if got := string(data[1]); got != input {
		t.Errorf("agent received %q for console 1, want %q", got, input)
	}

	if len(data) != 1 {
		t.Errorf("agent received input for consoles %v, want only console 1", data)
	}

	if len(ended) != 1 || ended[0] != 1 {
		t.Errorf("input ended for consoles %v, want [1]", ended)
	}

	if got, want := stdout.String(), "booting\n"+input; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}

	// The command came first, and no input preceded the open event: the agent
	// recorded the command and then only console traffic for console 1.
	agent.mu.Lock()
	first := agent.reqs[0].GetCommand()
	agent.mu.Unlock()

	if first == nil {
		t.Error("the first request was not the command")
	}
}

// Once a pipe is at its end, every console opened later is told at once, so a
// second console-reading module does not wait for input that cannot come.
func TestRunRPCPipeEndReachesLaterConsoles(t *testing.T) {
	agent := &scriptedAgent{}
	agent.run = func(stream runStreamServer) error {
		if _, err := agent.receive(stream); err != nil {
			return err
		}

		if err := agent.echoUntilEOF(stream, 1, pb.ConsoleMode_CONSOLE_MODE_LINE); err != nil {
			return err
		}

		return agent.echoUntilEOF(stream, 2, pb.ConsoleMode_CONSOLE_MODE_LINE)
	}

	app, stdout, _ := newConsoleApp(t, agent, strings.NewReader("one\n"), "")

	err := app.runRPC(context.Background(), "dev", "cmd", nil)
	if err != nil {
		t.Fatalf("runRPC: %v", err)
	}

	data, ended := agent.inputs()
	if got := string(data[1]); got != "one\n" {
		t.Errorf("console 1 received %q, want \"one\\n\"", got)
	}

	if len(ended) != 2 || ended[0] != 1 || ended[1] != 2 {
		t.Errorf("input ended for consoles %v, want [1 2]", ended)
	}

	if got := stdout.String(); got != "one\n" {
		t.Errorf("stdout = %q, want \"one\\n\"", got)
	}
}

// A file request answered by the receive routine while the send routine
// forwards input exercises the one sender both share; the race detector
// watches the stream.
func TestRunRPCFileReplyWhileInputFlows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "req.txt")

	if err := os.WriteFile(path, []byte("file content"), 0o600); err != nil {
		t.Fatal(err)
	}

	agent := &scriptedAgent{}
	agent.run = func(stream runStreamServer) error {
		if _, err := agent.receive(stream); err != nil {
			return err
		}

		if err := stream.Send(openRes(1, pb.ConsoleMode_CONSOLE_MODE_LINE)); err != nil {
			return err
		}

		if err := stream.Send(&pb.RunResponse{Msg: &pb.RunResponse_FileRequest{FileRequest: &pb.FileRequest{Path: path}}}); err != nil {
			return err
		}

		// The file reply and the input travel on separate routines of the
		// client, in no fixed order: wait for both, then end the console.
		var gotFile, gotEOF bool

		for !gotFile || !gotEOF {
			req, err := agent.receive(stream)
			if err != nil {
				return err
			}

			switch msg := req.GetMsg().(type) {
			case *pb.RunRequest_File:
				if string(msg.File.GetContent()) != "file content" {
					return errors.New("wrong file content")
				}

				gotFile = true
			case *pb.RunRequest_ConsoleControl:
				gotEOF = msg.ConsoleControl.GetEof() != nil
			}
		}

		return stream.Send(closeRes())
	}

	// Enough input for many chunks, so the sends overlap with the file reply.
	input := strings.Repeat("0123456789abcdef\n", 2048)

	app, _, _ := newConsoleApp(t, agent, strings.NewReader(input), "")

	err := app.runRPC(context.Background(), "dev", "cmd", nil)
	if err != nil {
		t.Fatalf("runRPC: %v", err)
	}

	data, _ := agent.inputs()
	if got := string(data[1]); got != input {
		t.Errorf("agent received %d bytes of input, want %d, intact", len(got), len(input))
	}
}

// Console output the agent sends without opening a console is still shown.
func TestRunRPCUnframedOutputShown(t *testing.T) {
	agent := &scriptedAgent{}
	agent.run = func(stream runStreamServer) error {
		if _, err := agent.receive(stream); err != nil {
			return err
		}

		return stream.Send(stdoutRes([]byte("stray output\n")))
	}

	app, stdout, _ := newConsoleApp(t, agent, strings.NewReader(""), "")

	err := app.runRPC(context.Background(), "dev", "cmd", nil)
	if err != nil {
		t.Fatalf("runRPC: %v", err)
	}

	if got := stdout.String(); got != "stray output\n" {
		t.Errorf("stdout = %q, want the stray output", got)
	}
}

// A run that ends while the user's input is still open leaves the send routine
// parked in its read of a pipe nobody writes to; the run itself must not wait
// for it. The console opens before the run ends, so the routine has entered
// its read, not a wait for a console, by the time the agent closes the console
// and ends the run.
func TestRunRPCEndsWithoutInputEnd(t *testing.T) {
	agent := &scriptedAgent{}
	agent.run = func(stream runStreamServer) error {
		if _, err := agent.receive(stream); err != nil {
			return err
		}

		if err := stream.Send(openRes(1, pb.ConsoleMode_CONSOLE_MODE_LINE)); err != nil {
			return err
		}

		// Give the send routine time to reach its read before the run ends.
		time.Sleep(50 * time.Millisecond)

		if err := stream.Send(&pb.RunResponse{Msg: &pb.RunResponse_Print{Print: &pb.Print{Text: []byte("done\n")}}}); err != nil {
			return err
		}

		return stream.Send(closeRes())
	}

	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	app, stdout, _ := newConsoleApp(t, agent, pr, "")

	done := make(chan error, 1)

	go func() { done <- app.runRPC(context.Background(), "dev", "cmd", nil) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runRPC: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runRPC did not return although the agent ended the run")
	}

	if got := stdout.String(); got != "done\n" {
		t.Errorf("stdout = %q, want \"done\\n\"", got)
	}
}
