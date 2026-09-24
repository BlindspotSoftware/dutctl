// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"io"
	"path/filepath"
	"runtime"
	"testing"
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
