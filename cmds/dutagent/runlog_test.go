// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/dutagent/session"
	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/pkg/module"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// logBuffer is a goroutine-safe log sink: the module goroutine logs while the
// test reads.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func TestLogRunEnd(t *testing.T) {
	canceled := connect.NewError(connect.CodeCanceled, context.Canceled)
	failed := connect.NewError(connect.CodeAborted, errors.New("module failed"))

	tests := []struct {
		name       string
		clientGone bool
		err        error
		want       string
	}{
		{"success", false, nil, `level=INFO msg="request finished successfully"`},
		{"client quit or lost", true, canceled, `level=INFO msg="request ended: client disconnected"`},
		{"module failure", false, failed, `level=ERROR msg="request finished with error"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer

			logRunEnd(slog.New(slog.NewTextHandler(&logs, nil)), tt.clientGone, tt.err)

			if !strings.Contains(logs.String(), tt.want) {
				t.Errorf("log = %q, want %q", logs.String(), tt.want)
			}
		})
	}
}

// untilCanceledModule stands in for a serial console: it runs until the client
// quits and treats that cancellation as its normal end. started is closed once
// it runs.
type untilCanceledModule struct {
	started chan struct{}
}

func (m *untilCanceledModule) Help() string                   { return "until canceled" }
func (m *untilCanceledModule) Init(_ context.Context) error   { return nil }
func (m *untilCanceledModule) Deinit(_ context.Context) error { return nil }
func (m *untilCanceledModule) Run(ctx context.Context, _ module.Session, _ ...string) error {
	close(m.started)
	<-ctx.Done()

	return nil
}

// TestExecuteModulesLogsCanceledRunAsStopped checks that a module ending with
// nil because the run was canceled is not logged as a successful finish.
func TestExecuteModulesLogsCanceledRunAsStopped(t *testing.T) {
	stream := &blockingStream{closed: make(chan struct{})}
	defer close(stream.closed)

	console := &untilCanceledModule{started: make(chan struct{})}
	mod := dut.Module{Module: console}
	mod.Config.Name = "untilCanceled"
	mod.Config.Passthrough = true

	logs := &logBuffer{}
	runCtx, cancelRun := context.WithCancel(log.Into(context.Background(), slog.New(slog.NewTextHandler(logs, nil))))
	broker := &session.Broker{}

	args := runCmdArgs{
		stream: stream,
		broker: broker,
		cmdMsg: &pb.Command{Device: "devX", Command: "serial"},
		cmd:    dut.Command{Modules: []dut.Module{mod}},
	}

	args, _, err := executeModules(runCtx, args)
	if err != nil {
		t.Fatalf("executeModules: %v", err)
	}

	<-console.started
	cancelRun() // the client quits

	select {
	case _, open := <-args.moduleErrCh:
		if open {
			t.Fatal("module reported an error, want a clean stop")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("modules did not stop after the run was canceled")
	}

	broker.Wait()

	got := logs.String()
	if !strings.Contains(got, "modules stopped: run canceled") {
		t.Errorf("log = %q, want the canceled run logged as stopped", got)
	}

	if strings.Contains(got, "all modules finished successfully") {
		t.Errorf("log = %q, want no success line for a canceled run", got)
	}
}
