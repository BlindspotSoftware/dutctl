// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// runAgent runs the agent with args until it calls its exit function and
// returns the exit code. start never returns on its own, so the injected exit
// function terminates the goroutine via runtime.Goexit, which still runs the
// deferred clean-up in start.
func runAgent(t *testing.T, args ...string) int {
	t.Helper()

	code := make(chan int, 1)

	go func() {
		exit := func(c int) {
			code <- c

			runtime.Goexit()
		}

		newAgent(io.Discard, exit, append([]string{"dutagent", "-log", "error"}, args...)).start()
	}()

	return <-code
}

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{
			name: "check-config valid",
			args: []string{"-check-config", "-c", filepath.Join("testdata", "valid_config.yaml")},
			want: 0,
		},
		{
			name: "check-config invalid",
			args: []string{"-check-config", "-c", filepath.Join("testdata", "invalid_config_empty_devices.yaml")},
			want: 1,
		},
		{
			name: "check-config missing file",
			args: []string{"-check-config", "-c", filepath.Join("testdata", "does-not-exist.yaml")},
			want: 1,
		},
		{
			name: "dry-run valid",
			args: []string{"-dry-run", "-c", filepath.Join("testdata", "valid_config.yaml")},
			want: 0,
		},
		{
			name: "dry-run module init fails",
			args: []string{"-dry-run", "-c", filepath.Join("testdata", "invalid_module_init_config.yaml")},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runAgent(t, tt.args...)
			if got != tt.want {
				t.Errorf("exit code: want %d, got %d", tt.want, got)
			}
		})
	}
}

// stopDuringInitModule sends the agent a stop signal from its Init and, once
// the signal has ended the startup context, returns as a module would that
// ignores its context.
type stopDuringInitModule struct{ lifecycleModule }

func (*stopDuringInitModule) Init(ctx context.Context) error {
	err := syscall.Kill(os.Getpid(), syscall.SIGTERM)
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("the stop signal did not end the startup context")
	}
}

func init() {
	module.Register(module.Record{ID: "test-stop-during-init", New: func() module.Module { return &stopDuringInitModule{} }})
}

// A stop signal during the startup ends it right after Init, with exit code 0,
// even if a module's Init ignored its context: the agent does not go on to
// register with a dutserver, which at an address nobody listens on would fail
// with exit code 1.
func TestStopSignalDuringStartup(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg.yaml")

	err := os.WriteFile(cfg, []byte(
		"version: 1.0.0-alpha.1\ndevices:\n  dev:\n    cmds:\n      cmd:\n        uses:\n          - module: test-stop-during-init\n",
	), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got := runAgent(t, "-c", cfg, "-server", "127.0.0.1:1")
	if got != 0 {
		t.Errorf("exit code = %d, want 0", got)
	}
}

// A stop signal during the registration with a dutserver ends the startup at
// once, with exit code 0: the registration is cancelled, not waited for.
func TestStopSignalDuringRegistration(t *testing.T) {
	// A dutserver that accepts the registration and, instead of answering,
	// sends the agent the stop signal.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}

		defer conn.Close()

		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM) // a failure shows as a registration that runs its full time
		_, _ = io.Copy(io.Discard, conn)
	}()

	started := time.Now()
	got := runAgent(t, "-c", filepath.Join("testdata", "valid_config.yaml"), "-server", ln.Addr().String())

	if got != 0 {
		t.Errorf("exit code = %d, want 0", got)
	}

	if took := time.Since(started); took > registerTimeout/2 {
		t.Errorf("the startup took %v, want the stop signal to end the registration well within registerTimeout", took)
	}
}

// cleanup bounds Deinit: a module whose Deinit ignores its context is left
// behind, and the agent exits with 1 for the unknown state. The parent
// context's shorter deadline stands in for deinitTimeout.
func TestCleanupLeavesStuckModuleBehind(t *testing.T) {
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.DiscardHandler))

	stuck := &stuckDeinitModule{release: make(chan struct{})}
	t.Cleanup(func() { close(stuck.release) })

	agt := &agent{modulesNeedDeinit: true}
	agt.config.Devices = dut.Devlist{"devA": {Cmds: map[string]dut.Command{
		"cmd": {Modules: []dut.Module{wrap("stuck", stuck)}},
	}}}

	code := make(chan int, 1)
	agt.exit = func(c int) {
		code <- c

		runtime.Goexit()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	go agt.cleanup(ctx, exit0)

	select {
	case got := <-code:
		if got != 1 {
			t.Errorf("exit code = %d, want 1 for a Deinit that did not finish", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup waited for a Deinit that ignores its context")
	}
}
