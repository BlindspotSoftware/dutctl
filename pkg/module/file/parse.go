// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package file

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	srcArg   = 0
	dstArg   = 1
	argCount = 2
)

// parseColonSyntax parses the argument for colon syntax (src:dest).
// Returns the src and dest parts, whether colon was present, and any error.
func parseColonSyntax(arg string) (string, string, error) {
	if !strings.Contains(arg, ":") {
		return "", "", nil
	}

	parts := strings.SplitN(arg, ":", argCount)

	if parts[srcArg] == "" {
		return "", "", errors.New("src path cannot be empty in 'src:dest' syntax")
	}

	if parts[dstArg] == "" {
		return "", "", errors.New("destination path cannot be empty in 'src:dest' syntax")
	}

	return parts[srcArg], parts[dstArg], nil
}

// determineSource returns the src path based on arg and colon parsing.
func determineSource(arg, colonSrc string) string {
	if colonSrc != "" {
		return colonSrc
	}

	return arg
}

// determineDestination returns the destination path following precedence rules:
// 1. destination config (if set) - always takes precedence
// 2. colon syntax destination (if present and no destination config)
// 3. working directory + basename.
func (f *File) determineDestination(colonDest string) string {
	// Rule 1: destination config takes precedence
	if f.Destination != "" {
		return f.Destination
	}

	// Rule 2: colon syntax destination
	if colonDest != "" {
		return colonDest
	}

	// Rule 3: set destination to working directory + src filename
	return filepath.Base(f.sourcePath)
}

// parsePaths parses the user argument and returns src and destination paths.
// destination config always represents the destination path when configured.
func (f *File) parsePaths(arg string) error {
	// Validate argument is not empty
	if arg == "" {
		return errors.New("path argument cannot be empty")
	}

	// Parse colon syntax
	colonSrc, colonDest, err := parseColonSyntax(arg)
	if err != nil {
		return err
	}

	// Check for mutually exclusive conditions: destination config and colon syntax
	if f.Destination != "" && colonDest != "" {
		return fmt.Errorf("cannot use colon syntax when destination is configured to %q", f.Destination)
	}

	// Determine src
	f.sourcePath = determineSource(arg, colonSrc)

	// Determine destination
	f.destPath = f.determineDestination(colonDest)

	return nil
}
