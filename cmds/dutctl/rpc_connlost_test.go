// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
	"github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1/dutctlv1connect"
)

// consoleAgent is a stub agent whose Run prints one console line, as a serial
// session does on connect, then ends the way the test chooses: with runErr, or,
// if runErr is nil, by staying open until the connection goes away.
type consoleAgent struct {
	dutctlv1connect.UnimplementedDeviceServiceHandler

	started chan struct{}
	runErr  error
}

func (a *consoleAgent) Run(ctx context.Context, stream *connect.BidiStream[pb.RunRequest, pb.RunResponse]) error {
	if _, err := stream.Receive(); err != nil { // the command
		return err
	}

	line := &pb.Console{Data: &pb.Console_Stdout{Stdout: []byte("--- Connected ---\n")}}
	if err := stream.Send(&pb.RunResponse{Msg: &pb.RunResponse_Console{Console: line}}); err != nil {
		return err
	}

	close(a.started)

	if a.runErr != nil {
		return a.runErr
	}

	<-ctx.Done()

	return ctx.Err()
}

// startAgent serves agent over h2c, as dutagent does.
func startAgent(t *testing.T, agent *consoleAgent) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(dutctlv1connect.NewDeviceServiceHandler(agent))

	srv := httptest.NewUnstartedServer(mux)
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetHTTP1(true)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	t.Cleanup(srv.Close)

	return srv
}

// runClient runs "dutctl <device> serial -i" against addr in the background and
// returns the channel its exit code arrives on.
func runClient(addr string, stderr *bytes.Buffer) <-chan int {
	exitCode := make(chan int, 1)
	app := newApp(strings.NewReader(""), &bytes.Buffer{}, stderr,
		func(code int) { exitCode <- code }, []string{"dutctl", "-s", addr, "dev", "serial", "-i"})

	go app.start()

	return exitCode
}

func waitExit(t *testing.T, exitCode <-chan int) int {
	t.Helper()

	select {
	case code := <-exitCode:
		return code
	case <-time.After(5 * time.Second):
		t.Fatal("dutctl did not exit")

		return 0
	}
}

func TestRunReportsLostConnection(t *testing.T) {
	agent := &consoleAgent{started: make(chan struct{})}
	srv := startAgent(t, agent)
	addr := srv.Listener.Addr().String()

	var stderr bytes.Buffer

	exitCode := runClient(addr, &stderr)

	<-agent.started
	srv.CloseClientConnections() // the network drops mid-session

	if code := waitExit(t, exitCode); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if want := "lost connection to dutagent at " + addr; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestRunKeepsAgentReportedError(t *testing.T) {
	agent := &consoleAgent{
		started: make(chan struct{}),
		runErr:  connect.NewError(connect.CodeAborted, errors.New("module failed: boom")),
	}
	srv := startAgent(t, agent)

	var stderr bytes.Buffer

	if code := waitExit(t, runClient(srv.Listener.Addr().String(), &stderr)); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if got := stderr.String(); !strings.Contains(got, "module failed: boom") || strings.Contains(got, "lost connection") {
		t.Errorf("stderr = %q, want the agent's own error, not a lost connection", got)
	}
}

// TestRunReportsUnreachableAgent checks the other side of the lost-connection
// rule: an agent that never answered is unreachable, not a lost connection.
func TestRunReportsUnreachableAgent(t *testing.T) {
	srv := startAgent(t, &consoleAgent{started: make(chan struct{})})
	addr := srv.Listener.Addr().String()
	srv.Close() // nothing listens at addr any more

	var stderr bytes.Buffer

	if code := waitExit(t, runClient(addr, &stderr)); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if got := stderr.String(); !strings.Contains(got, "cannot reach dutagent at "+addr) {
		t.Errorf("stderr = %q, want the agent reported as unreachable", got)
	}
}
