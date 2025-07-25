// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package shell provides a dutagent module that executes shell commands.
package shell

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
		ID:  "shell",
		New: func() module.Module { return &Shell{} },
	})
}

// DefaultShellPath is the default path to the shell executable on the dutagent.
const DefaultShellPath = "/bin/sh"

// Shell is a module that executes commands on the dutagent. It is non-interactive and does not support stdin.
// The shell command is executed with the -c flag and the command to execute as an argument.
type Shell struct {
	Path  string // Path is th path to the shell executable on the dutagent. If unset, [DefaultShellPath] is used.
	Quiet bool   // Quiet suppresses stdout from the shell command, stderr will be forwarded regardless.
}

// Ensure implementing the Module interface.
var _ module.Module = &Shell{}

const abstract = `Execute a shell command on the DUT agent
`
const usage = `
ARGUMENTS:
	[command-string]

`
const description = `
The shell is executed with the -c flag and the first argument to the module is passed as the command-string.
So make sure to quote the command-string if it contains spaces or special characters. E.g.: "ls -l /tmp"
The shell module is non-interactive and does not support stdin.
`

func (s *Shell) Help() string {
	log.Println("shell module: Help called")

	help := strings.Builder{}
	help.WriteString(abstract)
	help.WriteString(usage)
	help.WriteString(fmt.Sprintf("The used shell is %q.\n", s.Path))
	help.WriteString(description)

	if s.Quiet {
		help.WriteString("NOTE: The module is configured to quiet mode. Only stderr is forwarded.\n")
	}

	return help.String()
}

func (s *Shell) Init() error {
	log.Println("shell module: Init called")

	if s.Path == "" {
		s.Path = DefaultShellPath
	}

	_, err := exec.LookPath(s.Path)
	if err != nil {
		return fmt.Errorf("shell path %q: %w", s.Path, err)
	}

	return nil
}

func (s *Shell) Deinit() error {
	log.Println("shell module: Deinit called")

	return nil
}

func (s *Shell) Run(_ context.Context, sesh module.Session, args ...string) error {
	log.Println("shell module: Run called")

	if len(args) == 0 {
		return fmt.Errorf("missing command-string")
	}

	if len(args) > 1 {
		return fmt.Errorf("too many arguments - if the command-string contains spaces or special characters, quote it")
	}

	cmdStr := args[0]
	binary := s.Path

	shell := exec.Command(binary, "-c", cmdStr)

	out, err := shell.CombinedOutput()

	if !s.Quiet {
		// TODO: consider using sesh.Console() once raw mode is implemented
		sesh.Print(string(out))
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// Print stderr although the command is quiet
			if s.Quiet {
				sesh.Print(string(exitErr.Stderr))
				sesh.Print("\n")
			}

			return fmt.Errorf("shell command exited with code %d", exitErr.ExitCode())
		}

		return fmt.Errorf("failed to execute shell: %w", err)
	}

	return nil
}
