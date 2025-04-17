// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package flash provides a dutagent module that reads or writes the SPI flash on the DUT.
// This module is a wrapper around a flash tool that is executed on a dutagent.
package flash

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID:  "flash",
		New: func() module.Module { return &Flash{} },
	})
}

// DefaultFlashTool is the default tool on the dutagent.
const DefaultFlashTool = "/bin/flashrom"

// Flash is a module that reads or writes the SPI flash on the DUT.
type Flash struct {
	tool string
}

// Ensure implementing the Module interface.
var _ module.Module = &Flash{}

const abstract = `ToDo: add abstract
`
const usage = `
ARGUMENTS:
	[command-string]

`
const description = `
ToDo: add description
`

func (s *Flash) Help() string {
	log.Println("flash module: Help called")

	help := strings.Builder{}
	help.WriteString(abstract)
	help.WriteString(usage)
	help.WriteString(description)

	return help.String()
}

func (s *Flash) Init() error {
	log.Println("flash module: Init called")

	if s.tool == "" {
		s.tool = DefaultFlashTool
	}

	_, err := exec.LookPath(s.tool)
	if err != nil {
		return fmt.Errorf("flash tool %q: %w", s.tool, err)
	}

	return nil
}

func (s *Flash) Deinit() error {
	log.Println("flash module: Deinit called")

	return nil
}

func (s *Flash) Run(_ context.Context, sesh module.Session, args ...string) error {
	log.Println("flash module: Run called")

	cmdStr := args[0]
	flashtool := s.tool

	shell := exec.Command(flashtool, "-c", cmdStr)

	_, err := shell.CombinedOutput()

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			sesh.Print(string(exitErr.Stderr))
			sesh.Print("\n")

			return fmt.Errorf("flash tool exited with code %d", exitErr.ExitCode())
		}

		return fmt.Errorf("failed to execute %s: %w", s.tool, err)
	}

	return nil
}
