// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package procexec builds cancellable external commands whose entire process
// group is torn down on cancellation. A plain exec.CommandContext signals only
// the direct child, so a command that forks (a shell, a wrapper tool) leaves
// grandchildren orphaned and, because they inherit the command's output pipes,
// can keep Wait blocked until they exit. Running the command in its own process
// group and signalling that group on cancellation avoids both.
package procexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// DefaultGrace is a WaitDelay backstop suitable for a group-SIGKILL cancel: the
// group dies at once, so the delay only guards the rare process wedged in an
// uninterruptible state. A graceful (SIGTERM) canceller should pass a longer
// grace sized to how long the tool needs to stop cleanly.
const DefaultGrace = 5 * time.Second

// Command returns an *exec.Cmd bound to ctx that runs in its own process group.
// When ctx is cancelled, the whole group is signalled with cancelSig (so forked
// children die too); if the command has not exited within cancelGrace, os/exec
// force-closes its I/O and kills the direct process as a backstop. Use SIGKILL
// for an unconditional stop, or SIGTERM with a longer grace to let the tool shut
// down cleanly first.
func Command(ctx context.Context, cancelSig syscall.Signal, cancelGrace time.Duration, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)

	// Put the command in its own process group so a single signal reaches it and
	// everything it spawns.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	cmd.WaitDelay = cancelGrace

	cmd.Cancel = func() error {
		// A negative PID targets the whole process group created by Setpgid.
		err := syscall.Kill(-cmd.Process.Pid, cancelSig)
		if errors.Is(err, syscall.ESRCH) {
			// The group is already gone; tell os/exec the process is done rather
			// than surfacing a spurious cancel error.
			return os.ErrProcessDone
		}

		return err
	}

	return cmd
}
