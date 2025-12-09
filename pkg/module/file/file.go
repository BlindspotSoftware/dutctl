// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package file provides a dutagent module that transfers files between client and dutagent.
package file

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID: "file",
		New: func() module.Module {
			return &File{}
		},
	})
}

// File permission constants.
const (
	defaultDirPerm  = 0o755 // Default directory permissions
	defaultFilePerm = 0o644 // Default file permissions
)

// op represents the file operation.
type op string

const (
	opUpload   op = "upload"
	opDownload op = "download"
)

// File is a module that transfers files between client and dutagent.
type File struct {
	// ForceDir forces creation of parent directories if they don't exist (default: false).
	ForceDir bool
	// Overwrite allows overwriting existing files (default: false).
	Overwrite bool
	// Perm sets file permissions in octal format (e.g., "0644", "0755").
	Perm string
	// Operation pre-configures the operation type ("upload" or "download").
	Operation string
	// Destination is the destination path that overrides any colon syntax in arguments.
	Destination string

	sourcePath string // sourcePath is the source file path
	destPath   string // destPath is the destination file path
}

// Ensure implementing the Module interface.
var _ module.Module = &File{}

const abstract = `Transfer files between client and dutagent.`

const usage = `
ARGUMENTS:
	[path]
	[source:destination]

The operation type (upload or download) must be configured in the device YAML.

For single path form:
  - If destination is configured: uses configured destination
  - If destination not configured: uses working directory + basename

For colon syntax:
  - Explicitly specifies both source and destination paths
  - Only works if destination is NOT configured
  
For upload: <source> is client path, <destination> is dutagent path
For download: <source> is dutagent path, <destination> is client path
`

const description = `
See the README for detailed configuration options and examples.
`

func (f *File) Help() string {
	log.Println("file module: Help called")

	help := strings.Builder{}
	help.WriteString(abstract)
	help.WriteString(usage)
	help.WriteString(description)

	return help.String()
}

func (f *File) Init() error {
	log.Println("file module: Init called")

	// Validate configuration
	return f.validateConfig()
}

func (f *File) Deinit() error {
	log.Println("file module: Deinit called")

	return nil
}

func (f *File) Run(_ context.Context, sesh module.Session, args ...string) error {
	log.Println("file module: Run called")

	// Validate arguments
	err := f.validateArguments(args)
	if err != nil {
		return err
	}

	// Parse arguments
	err = f.parsePaths(args[0])
	if err != nil {
		return err
	}

	switch f.Operation {
	case string(opUpload):
		return f.uploadFile(sesh)
	case string(opDownload):
		return f.downloadFile(sesh)
	default:
		return fmt.Errorf("invalid operation %q: must be 'upload' or 'download'", f.Operation)
	}
}

// uploadFile handles uploading a file from client to dutagent.
func (f *File) uploadFile(sesh module.Session) error {
	log.Printf("file module: Uploading %q from client to %q on dutagent", f.sourcePath, f.destPath)

	// Request file from client
	fileReader, err := sesh.RequestFile(f.sourcePath)
	if err != nil {
		return fmt.Errorf("failed to request file from client: %w", err)
	}

	// Check if destination exists
	if !f.Overwrite {
		_, err := os.Stat(f.destPath)
		if err == nil {
			return fmt.Errorf("destination file %q already exists (use overwrite option to replace)", f.destPath)
		}
	}

	// Create parent directories if needed
	if f.ForceDir {
		destDir := filepath.Dir(f.destPath)

		err := os.MkdirAll(destDir, defaultDirPerm)
		if err != nil {
			return fmt.Errorf("failed to create parent directories for %q: %w", f.destPath, err)
		}

		log.Printf("file module: Created parent directories for %q", f.destPath)
	}

	// Determine file permissions
	perm := fs.FileMode(defaultFilePerm) // default

	if f.Perm != "" {
		mode, err := strconv.ParseUint(f.Perm, 8, 32)
		if err != nil {
			return fmt.Errorf("failed to parse mode %q: %w", f.Perm, err)
		}

		perm = fs.FileMode(mode)
	}

	// Create destination file
	destFile, err := os.OpenFile(f.destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("failed to create destination file %q: %w", f.destPath, err)
	}
	defer destFile.Close()

	// File data
	bytesWritten, err := io.Copy(destFile, fileReader)
	if err != nil {
		return fmt.Errorf("failed to write file data: %w", err)
	}

	log.Printf("file module: Successfully uploaded %d bytes to %q", bytesWritten, f.destPath)
	sesh.Printf("Upload complete: %s -> %s (%d bytes)\n", f.sourcePath, f.destPath, bytesWritten)

	return nil
}

// downloadFile handles downloading a file from dutagent to client.
func (f *File) downloadFile(sesh module.Session) error {
	log.Printf("file module: Downloading %q from dutagent to %q on client", f.sourcePath, f.destPath)

	// Validate source file exists
	fileInfo, err := os.Stat(f.sourcePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("source file %q not found on dutagent", f.sourcePath)
		}

		return fmt.Errorf("failed to open source file %q: %w", f.sourcePath, err)
	}

	// Open source file
	srcFile, err := os.Open(f.sourcePath)
	if err != nil {
		return fmt.Errorf("failed to open source file %q: %w", f.sourcePath, err)
	}
	defer srcFile.Close()

	// Send file to client
	err = sesh.SendFile(f.destPath, srcFile)
	if err != nil {
		return fmt.Errorf("failed to send file to client: %w", err)
	}

	log.Printf("file module: Successfully downloaded %d bytes from %q", fileInfo.Size(), f.sourcePath)
	sesh.Printf("Download complete: %s -> %s (%d bytes)\n", f.sourcePath, f.destPath, fileInfo.Size())

	return nil
}
