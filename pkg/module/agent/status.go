// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"strings"
	"syscall"

	"github.com/BlindspotSoftware/dutctl/internal/procexec"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID:  "agent-status",
		New: func() module.Module { return &Status{} },
	})
}

// Status prints status information about the system on which dutagent is running.
type Status struct{}

// Ensure implementing the Module interface.
var _ module.Module = &Status{}

func (m *Status) Help() string {
	return "Get status information about the system on which dutagent is running. No Arguments required."
}

func (m *Status) Init(_ context.Context) error {
	return nil
}

func (m *Status) Deinit(_ context.Context) error {
	return nil
}

// Run executes "uname -a" on the host running dutagent and prints its output to the session. Any arguments are ignored.
func (m *Status) Run(ctx context.Context, s module.Session, _ ...string) error {
	var out strings.Builder

	// Run in its own process group so a cancelled Run kills the process (and any
	// children) rather than leaving it running.
	cmd := procexec.Command(ctx, syscall.SIGKILL, procexec.DefaultGrace, "uname", "-a")
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to run uname: %v", err)
	}

	s.Println(out.String())

	return nil
}
