// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package script provides a dutagent module that executes script files.
package script

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID:  "script",
		New: func() module.Module { return &Script{} },
	})
}

// Script is a module that executes a configured script file with runtime arguments.
type Script struct {
	Path        string // Path is the absolute path to the script file (required)
	Interpreter string // Interpreter is optional interpreter path (e.g., /bin/bash). If empty, script must be executable.
	Quiet       bool   // Quiet suppresses stdout, stderr will be forwarded regardless
}

// Ensure implementing the Module interface.
var _ module.Module = &Script{}

const abstract = `Execute a configured script file on the DUT agent
`

const usage = `
ARGUMENTS:
	[arg1] [arg2] ...

`

const description = `
The script path is configured in the module options.
All arguments passed at runtime are forwarded to the script.

The module returns an error if the script exits with a non-zero exit code.
Script stdout is printed unless quiet mode is enabled.
Script stderr is always printed regardless of quiet mode.

Example config:
power:
  modules:
    - module: script
      options:
        path: "/opt/scripts/power-control.sh"
        interpreter: "/bin/bash"
        quiet: false

Example usage:
  dutctl device power "on"
  
This executes: /bin/bash /opt/scripts/power-control.sh on

The script's working directory is set to the script's parent directory.
The script module is non-interactive.
`

func (s *Script) Help() string {
	log.Println("script module: Help called")

	help := strings.Builder{}
	help.WriteString(abstract)
	help.WriteString(usage)
	help.WriteString(fmt.Sprintf("Script: %s\n", s.Path))

	if s.Interpreter != "" {
		help.WriteString(fmt.Sprintf("Interpreter: %s\n", s.Interpreter))
	} else {
		help.WriteString("No interpreter configured - script must be executable\n")
	}

	help.WriteString(description)

	if s.Quiet {
		help.WriteString("NOTE: The module is configured to quiet mode. Only stderr is forwarded.\n")
	}

	return help.String()
}

func (s *Script) Init() error {
	log.Println("script module: Init called")

	// Validate Path is set
	if s.Path == "" {
		return fmt.Errorf("path must be configured")
	}

	// Validate script exists and is regular file
	stat, err := os.Stat(s.Path)
	if err != nil {
		return fmt.Errorf("script path %q: %w", s.Path, err)
	}

	if stat.IsDir() {
		return fmt.Errorf("script path %q is a directory, not a file", s.Path)
	}

	if !stat.Mode().IsRegular() {
		return fmt.Errorf("script path %q is not a regular file", s.Path)
	}

	// Check executable bit only when no interpreter configured
	if s.Interpreter == "" {
		if stat.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("script %q is not executable (missing +x permission)", s.Path)
		}
	}

	// Validate interpreter if configured
	if s.Interpreter != "" {
		_, err := exec.LookPath(s.Interpreter)
		if err != nil {
			return fmt.Errorf("interpreter %q: %w", s.Interpreter, err)
		}
	}

	return nil
}

func (s *Script) Deinit() error {
	log.Println("script module: Deinit called")

	return nil
}

//nolint:gosec // G204: Script path validated in Init, args from client by design
func (s *Script) Run(ctx context.Context, sesh module.Session, args ...string) error {
	log.Println("script module: Run called")

	log.Printf("script module: Executing %s with %d arguments", s.Path, len(args))

	var cmd *exec.Cmd

	if s.Interpreter != "" {
		// Use interpreter: interpreter scriptPath arg1 arg2 ...
		cmdArgs := append([]string{s.Path}, args...)
		cmd = exec.CommandContext(ctx, s.Interpreter, cmdArgs...)
	} else {
		// Direct execution: scriptPath arg1 arg2 ...
		cmd = exec.CommandContext(ctx, s.Path, args...)
	}

	// Set working directory to script's directory
	cmd.Dir = filepath.Dir(s.Path)

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
			return fmt.Errorf("script exited with code %d", exitErr.ExitCode())
		}

		return fmt.Errorf("failed to execute script: %w", err)
	}

	return nil
}
