// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package file

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

const (
	srcArg        = 0
	dstArg        = 1
	colonArgCount = 2
)

// parsePaths parses the user argument and returns src and destination paths.
// destination always represents the destination path when configured.
func (f *File) parsePaths(args []string) error {
	arg, err := f.evalArgs(args)
	if err != nil {
		return err
	}

	// Initialize source and destination with configured values
	f.sourcePath = f.Source
	f.destPath = f.Destination

	// If both source and destination are configured, arg must be empty
	if f.Source != "" && f.Destination != "" {
		if arg != "" {
			return fmt.Errorf("no path expected when both source and destination are configured")
		}

		return nil
	}

	// Validate argument is not empty (required when not both configs are set)
	if arg == "" {
		if f.sourcePath != "" {
			return errors.New("destination path cannot be empty")
		}

		return errors.New("source path cannot be empty")
	}

	// Check if colon syntax is used
	if !strings.Contains(arg, ":") {
		return f.parseSinglePath(arg)
	}

	return f.parseColonPath(arg)
}

// evalArgs parses and validates arguments using flag.FlagSet.
func (f *File) evalArgs(args []string) (string, error) {
	fs := flag.NewFlagSet("file", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	err := fs.Parse(args)
	if err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	// If both source and destination are configured, no path is expected
	if f.Source != "" && f.Destination != "" {
		if fs.NArg() != 0 {
			return "", fmt.Errorf("no path expected when both source and destination are configured")
		}

		return "", nil // Empty string is valid when both are configured
	}

	// Otherwise, expect exactly one positional argument
	if fs.NArg() != 1 {
		if fs.NArg() == 0 {
			return "", errors.New("missing argument: path required")
		}

		return "", fmt.Errorf("invalid argument count: expected 1 arg, got %d", fs.NArg())
	}

	return fs.Arg(0), nil
}

// parseSinglePath handles single path argument (no colon syntax).
// Determines source and destination based on configured values.
func (f *File) parseSinglePath(arg string) error {
	switch {
	case f.sourcePath != "":
		// Source is configured, arg is destination - sanitize
		destPath, err := sanitizePath(arg)
		if err != nil {
			return err
		}

		f.destPath = destPath
	case f.destPath != "":
		// Destination is configured, arg is source - keep as-is
		f.sourcePath = arg
	default:
		// Neither configured, arg is source, sanitize for destination
		f.sourcePath = arg

		f.destPath = filepath.Base(arg)
	}

	return nil
}

// parseColonPath parses the argument for colon syntax (src:dest).
func (f *File) parseColonPath(arg string) error {
	// Colon syntax used - check mutual exclusivity with config
	if f.Source != "" {
		return fmt.Errorf("cannot use colon syntax when source is configured: %q", f.Source)
	}

	if f.Destination != "" {
		return fmt.Errorf("cannot use colon syntax when destination is configured: %q", f.Destination)
	}

	parts := strings.Split(arg, ":")
	if len(parts) != colonArgCount {
		return fmt.Errorf("colon syntax requires exactly one colon")
	}

	if parts[srcArg] == "" {
		return errors.New("src path cannot be empty in 'src:dest' syntax")
	}

	if parts[dstArg] == "" {
		return errors.New("destination path cannot be empty in 'src:dest' syntax")
	}

	// Sanitize both parts
	sourcePath, err := sanitizePath(parts[srcArg])
	if err != nil {
		return err
	}

	destPath, err := sanitizePath(parts[dstArg])
	if err != nil {
		return err
	}

	f.sourcePath = sourcePath
	f.destPath = destPath

	return nil
}

// sanitizePath cleans and secures a user-provided path.
// - Normalizes path separators and removes redundant elements
// - Returns error for absolute paths
// - Returns error for paths with leading ..
// - For other relative paths, preserves directory structure.
func sanitizePath(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	cleaned := filepath.Clean(path)

	// Reject absolute paths
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("absolute paths are not allowed: %q", path)
	}

	// Reject paths with leading ..
	if strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("paths with leading '..' are not allowed: %q", path)
	}

	// Relative paths preserve directory structure
	return cleaned, nil
}
