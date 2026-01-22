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
	DefaultDirPerm  = 0o755 // Default directory permissions
	DefaultFilePerm = 0o644 // Default file permissions
)

// op represents the file operation.
type op string

const (
	opUpload   op = "upload"
	opDownload op = "download"
)

// File is a module that transfers files between client and dutagent.
type File struct {
	// Permission sets file permissions in octal format (e.g., "0644", "0755").
	// Defaults: [DefaultFilePerm] for files, [DefaultDirPerm] for directories.
	Permission string
	// Operation pre-configures the operation type ("upload" or "download").
	Operation string

	// Source and Destination are optional config inputs.
	// If set, the command-line arg becomes the counterpart (dest or src respectively).
	Source      string
	Destination string

	// sourcePath is the final path combined path from config and args
	// used for the actual internal file transfer operation.
	sourcePath string
	// destPath is the final path combined path from config and args
	// used for the actual internal file transfer operation.
	destPath string
}

// Ensure implementing the Module interface.
var _ module.Module = &File{}

const abstract = `Transfer files between client and dutagent.
`

const (
	usageBothConfigured = `
`
	usageSourceConfigured = `
ARGUMENTS:
	DST

`
	usageDestConfigured = `
ARGUMENTS:
	SRC

`
	usageNeitherConfigured = `
ARGUMENTS:
	SRC[:DEST]

`
)

func (f *File) Help() string {
	log.Println("file module: Help called")

	help := strings.Builder{}
	help.WriteString(abstract)

	help.WriteString(usageAndDescription(f.Operation, f.Source, f.Destination))

	help.WriteString(fmt.Sprintf("File permission will be set to %q.\n", f.Permission))

	return help.String()
}

//nolint:cyclop
func usageAndDescription(operation, source, destination string) string {
	var description string

	switch {
	case source != "" && destination != "":
		// Both configured
		switch operation {
		case string(opUpload):
			description = fmt.Sprintf("Copy from %s on client to %s on dutagent.\n", source, destination)
		case string(opDownload):
			description = fmt.Sprintf("Copy from %s on dutagent to %s on client.\n", source, destination)
		}

		return usageBothConfigured + description
	case source != "":
		// Source configured, ARG -> destination
		switch operation {
		case string(opUpload):
			description = fmt.Sprintf("Copy from %s on client to DST on dutagent.\n", source)
		case string(opDownload):
			description = fmt.Sprintf("Copy from %s on dutagent to DST on client.\n", source)
		}

		return usageSourceConfigured + description
	case destination != "":
		// Destination configured, ARG -> source
		switch operation {
		case string(opUpload):
			description = fmt.Sprintf("Copy from SRC on client to %s on dutagent.\n", destination)
		case string(opDownload):
			description = fmt.Sprintf("Copy from SRC on dutagent to %s on client.\n", destination)
		}

		return usageDestConfigured + description
	default:
		switch operation {
		case string(opUpload):
			description = "Copy from SRC on client to DST on dutagent.\nIf DST is omitted the original file name is used.\n"
		case string(opDownload):
			description = "Copy from SRC on dutagent to DST on client.\nIf DST is omitted the original file name is used.\n"
		}

		return usageNeitherConfigured + description
	}
}

func (f *File) Init() error {
	log.Println("file module: Init called")

	if f.Permission != "" {
		// Permission must start with "0" to indicate octal format
		if f.Permission[0] != '0' {
			return fmt.Errorf("invalid permission %q: must be octal format starting with '0' (e.g., '0644')", f.Permission)
		}

		_, err := strconv.ParseUint(f.Permission, 8, 32)
		if err != nil {
			return fmt.Errorf("invalid permission %q: must be octal format (e.g., '0644'): %w", f.Permission, err)
		}
	} else {
		f.Permission = "0" + strconv.FormatInt(int64(DefaultFilePerm), 8)
	}

	// Validate operation is set
	if f.Operation == "" {
		return fmt.Errorf("operation must be set in config: must be 'upload' or 'download'")
	}

	// Validate operation value
	if f.Operation != string(opUpload) && f.Operation != string(opDownload) {
		return fmt.Errorf("invalid operation %q: must be 'upload' or 'download'", f.Operation)
	}

	return nil
}

func (f *File) Deinit() error {
	log.Println("file module: Deinit called")

	return nil
}

func (f *File) Run(_ context.Context, sesh module.Session, args ...string) error {
	log.Println("file module: Run called")

	// Parse paths
	err := f.parsePaths(args)
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

	// Create parent directories
	destDir := filepath.Dir(f.destPath)

	err = os.MkdirAll(destDir, DefaultDirPerm)
	if err != nil {
		return fmt.Errorf("failed to create parent directories for %q: %w", f.destPath, err)
	}

	mode, err := strconv.ParseUint(f.Permission, 8, 32)
	if err != nil {
		return fmt.Errorf("failed to parse permission %q: %w", f.Permission, err)
	}

	perm := fs.FileMode(mode)

	// Create destination file
	dstFile, err := os.OpenFile(f.destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("failed to create destination file %q: %w", f.destPath, err)
	}

	var copyErr error

	defer func() {
		dstFile.Close()

		if copyErr != nil {
			os.Remove(f.destPath)
		}
	}()

	// File data
	bytesWritten, copyErr := io.Copy(dstFile, fileReader)
	if copyErr != nil {
		return fmt.Errorf("failed to write file data: %w", copyErr)
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

	// Open source file.
	// Note: the session takes ownership of the file via SendFile and closes it
	// when the transfer completes, so we must NOT defer Close here.
	srcFile, err := os.Open(f.sourcePath)
	if err != nil {
		return fmt.Errorf("failed to open source file %q: %w", f.sourcePath, err)
	}

	// Send file to client with size information
	err = sesh.SendFile(f.destPath, fileInfo.Size(), srcFile)
	if err != nil {
		return fmt.Errorf("failed to send file to client: %w", err)
	}

	log.Printf("file module: Successfully downloaded %d bytes from %q", fileInfo.Size(), f.sourcePath)
	sesh.Printf("Download complete: %s -> %s (%d bytes)\n", f.sourcePath, f.destPath, fileInfo.Size())

	return nil
}
