// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package shell provides a dutagent module that executes shell commands.
package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
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

// Shell is a module that executes commands or scripts on the dutagent. It is non-interactive and does not support stdin.
// When Path is a shell, commands are executed with the -c flag.
// When Path is a script file, arguments are passed directly to the script.
type Shell struct {
	Path  string // Path is the executable path (shell or script). If unset, [DefaultShellPath] is used.
	Quiet bool   // Quiet suppresses stdout, stderr will be forwarded regardless.
}

// Ensure implementing the Module interface.
var _ module.Module = &Shell{}

const abstract = `Execute shell commands or scripts on the DUT agent
`

const usage = `
ARGUMENTS:
	Command mode (when Path is a shell): [command-string]
	Script mode (when Path is a script): [arg1] [arg2] ...

`

const description = `
The module operates in two modes depending on the configured Path:

COMMAND MODE (Path is a shell like sh, bash, zsh, etc.):
  The shell is executed with the -c flag and the first argument is passed as the command-string.
  Quote the command-string if it contains spaces or special characters. E.g.: "ls -l /tmp"
  Example config:
    test:
      modules:
        - module: shell
          options:
            path: "/bin/bash"
  Example usage: dutctl device test "ls -la"

SCRIPT MODE (Path is a script file):
  The script is executed directly with all runtime arguments passed to it.
  The script's working directory is set to the script's parent directory.
  Example config:
    power:
      modules:
        - module: shell
          options:
            path: "/opt/scripts/power-control.sh"
  Example usage: dutctl device power "on"

The shell module is non-interactive and does not support stdin.
`

func (s *Shell) Help() string {
	log.Println("shell module: Help called")

	help := strings.Builder{}
	help.WriteString(abstract)
	help.WriteString(usage)

	if isShell(s.Path) {
		help.WriteString(fmt.Sprintf("Mode: COMMAND MODE (Path %q is a shell)\n", s.Path))
	} else {
		help.WriteString(fmt.Sprintf("Mode: SCRIPT MODE (Path %q is a script)\n", s.Path))
	}

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

	// Check if Path exists using exec.LookPath (works for both scripts and shells)
	_, err := exec.LookPath(s.Path)
	if err != nil {
		return fmt.Errorf("executable path %q: %w", s.Path, err)
	}

	return nil
}

func (s *Shell) Deinit() error {
	log.Println("shell module: Deinit called")

	return nil
}

//nolint:gosec // G204: Script path validated in Init, args from client by design
func (s *Shell) Run(ctx context.Context, sesh module.Session, args ...string) error {
	log.Println("shell module: Run called")

	if len(args) == 0 {
		return fmt.Errorf("missing arguments")
	}

	var cmd *exec.Cmd

	// Detect mode based on whether Path is a shell
	if isShell(s.Path) {
		// Command mode: execute shell with -c flag
		if len(args) > 1 {
			return fmt.Errorf("too many arguments for command mode - if the command-string contains spaces or special characters, quote it")
		}

		log.Printf("shell module: Executing command mode: %s -c %q", s.Path, args[0])
		cmd = exec.CommandContext(ctx, s.Path, "-c", args[0])
	} else {
		// Script mode: execute Path directly with arguments
		log.Printf("shell module: Executing script mode: %s with %d arguments", s.Path, len(args))
		cmd = exec.CommandContext(ctx, s.Path, args...)
		cmd.Dir = filepath.Dir(s.Path)
	}

	// Capture stdout and stderr separately
	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Execute command
	err := cmd.Run()

	// Print stdout unless quiet mode is enabled
	if !s.Quiet && stdout.Len() > 0 {
		sesh.Print(stdout.String())
	}

	// Always print stderr (error messages should always be visible)
	if stderr.Len() > 0 {
		sesh.Print(stderr.String())
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("command exited with code %d", exitErr.ExitCode())
		}

		return fmt.Errorf("failed to execute command: %w", err)
	}

	return nil
}

// isShell returns true if the given path is a known shell executable.
func isShell(path string) bool {
	basename := filepath.Base(path)
	shells := []string{"sh", "bash", "zsh", "dash", "ksh", "csh", "tcsh", "fish"}

	for _, shell := range shells {
		if basename == shell {
			return true
		}
	}

	return false
}
