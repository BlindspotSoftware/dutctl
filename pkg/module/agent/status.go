// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"

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
	log.Println("agent.Status module: Help called")

	return "Get status information about the system on which dutagent is running. No Arguments required."
}

func (m *Status) Init() error {
	log.Println("agent.Status module: Init called")

	return nil
}

func (m *Status) Deinit() error {
	log.Println("agent.Status module: Deinit called")

	return nil
}

func (m *Status) Run(_ context.Context, s module.Session, _ ...string) error {
	log.Println("agent.Status module: Run called")

	var out strings.Builder

	//nolint:noctx
	cmd := exec.Command("uname", "-a")
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to run uname: %v", err)
	}

	s.Println(out.String())

	return nil
}
