// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package procexec

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"testing"
	"time"
)

// TestCommandKillsProcessGroup verifies the load-bearing behaviour: cancelling a
// command's context tears down not just the direct process but its whole process
// group, so a forked child does not survive as an orphan. A plain
// exec.CommandContext would leave the child running (and, via the inherited
// pipe, could block Wait until it exits).
func TestCommandKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The shell spawns a background sleep, prints its PID, then waits on it. The
	// sleep is a separate process in the command's group; a group kill must reach
	// it too.
	cmd := Command(ctx, syscall.SIGKILL, DefaultGrace, "sh", "-c", "sleep 60 & echo $!; wait")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var childPID int
	if _, err := fmt.Fscanf(stdout, "%d", &childPID); err != nil {
		t.Fatalf("reading child pid: %v", err)
	}

	// The child is alive right after start (signal 0 probes existence).
	if err := syscall.Kill(childPID, 0); err != nil {
		t.Fatalf("child %d not alive after start: %v", childPID, err)
	}

	cancel()

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	select {
	case <-waitErr:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return promptly after cancel")
	}

	// The child must be gone: a group kill reaped it, not orphaned it. Poll to let
	// the signal propagate and init reap the reparented process.
	deadline := time.After(2 * time.Second)

	for {
		if err := syscall.Kill(childPID, 0); errors.Is(err, syscall.ESRCH) {
			return // child dead — the process group was killed
		}

		select {
		case <-deadline:
			t.Fatalf("child %d survived cancellation (process group not killed)", childPID)
		case <-time.After(20 * time.Millisecond):
		}
	}
}
