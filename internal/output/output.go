// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package output provides interfaces and implementations for different output formats.
package output

import (
	"io"
)

// ContentType is an identifier for different kinds of formatted output.
type ContentType string

const (
	// TypeGeneral represents general text output.
	TypeGeneral ContentType = "general"

	// TypeDeviceList represents a list of available devices.
	TypeDeviceList ContentType = "device-list"

	// TypeCommandList represents a list of commands for a device.
	TypeCommandList ContentType = "command-list"

	// TypeCommandDetail represents detailed help for a command.
	TypeCommandDetail ContentType = "command-detail"

	// TypeModuleOutput represents output from a command execution.
	TypeModuleOutput ContentType = "module-output"

	// TypeVersion represents version information.
	TypeVersion ContentType = "version"
)

// Content is a structured data unit to be formatted and displayed.
type Content struct {
	// Type identifies the category of this content.
	Type ContentType

	// Data holds the actual content, which can be a string or structured data like []string.
	Data interface{}

	// IsError indicates whether this content represents an error.
	IsError bool

	// Metadata contains additional contextual information about the content.
	// Common keys include:
	// - "server": Address of the dutagent server
	// - "msg": message or description of remote procedure call
	// - "device": Target device identifier
	// - "command": Command being executed
	// - "args": Command arguments
	Metadata map[string]string
}

// Formatter provides methods to format and output content in different styles.
type Formatter interface {
	// WriteContent formats and outputs a structured content object.
	WriteContent(content Content)

	// Write sends plain text to standard output as a convenience method.
	Write(text string)

	// WriteErr sends plain text to standard error as a convenience method.
	WriteErr(text string)

	// Buffer enables output buffering mode, accumulating content instead of immediate output.
	Buffer()

	// IsBuffering returns whether the formatter is currently in buffering mode.
	IsBuffering() bool

	// Flush writes all buffered content and returns to immediate mode.
	Flush() error
}

// Config contains the configuration options for output formatters.
type Config struct {
	// Stdout is the writer for standard output.
	Stdout io.Writer

	// Stderr is the writer for standard error.
	Stderr io.Writer

	// NoColor disables colored output when set to true.
	NoColor bool

	// Format specifies the output format (text, json, yaml).
	Format string

	// Verbose enables additional details in the output.
	Verbose bool
}

// New creates an appropriate output formatter based on the provided configuration.
//
//nolint:ireturn
func New(config Config) Formatter {
	switch config.Format {
	case "json":
		return newJSONFormatter(config)
	case "yaml":
		return newYAMLFormatter(config)
	case "csv", "oneline":
		return newOneLineFormatter(config)
	default:
		return newTextFormatter(config)
	}
}
