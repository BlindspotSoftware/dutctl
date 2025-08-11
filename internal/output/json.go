// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package output

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"time"
)

// JSONFormatter formats output as JSON objects.
type JSONFormatter struct {
	stdout     io.Writer
	stderr     io.Writer
	verbose    bool
	buffering  bool
	bufferList []JSONOutput
}

// JSONOutput is a struct for JSON formatted output.
type JSONOutput struct {
	ContentType string            `json:"contentType"`
	Data        interface{}       `json:"data"`
	Error       bool              `json:"error,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Timestamp   string            `json:"timestamp"`
}

// newJSONFormatter creates a new JSON formatter.
func newJSONFormatter(config Config) *JSONFormatter {
	return &JSONFormatter{
		stdout:     config.Stdout,
		stderr:     config.Stderr,
		verbose:    config.Verbose,
		buffering:  false,
		bufferList: make([]JSONOutput, 0),
	}
}

// WriteContent formats and outputs structured content.
func (f *JSONFormatter) WriteContent(content Content) {
	output := JSONOutput{
		ContentType: string(content.Type),
		Data:        content.Data,
		Error:       content.IsError,
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	// Only include metadata if verbose mode is enabled
	if f.verbose && len(content.Metadata) > 0 {
		output.Metadata = content.Metadata
	}

	// If buffering, add to buffer list and return
	if f.buffering {
		f.bufferList = append(f.bufferList, output)

		return
	}

	// Immediate output
	writer := f.stdout
	if content.IsError {
		writer = f.stderr
	}

	bytes, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		log.Printf("Error marshaling JSON: %v", err)

		return
	}

	fmt.Fprintln(writer, string(bytes))
}

// Write sends text to standard output.
func (f *JSONFormatter) Write(text string) {
	f.WriteContent(Content{
		Type: TypeGeneral,
		Data: text,
	})
}

// WriteErr sends text to standard error.
func (f *JSONFormatter) WriteErr(text string) {
	f.WriteContent(Content{
		Type:    TypeGeneral,
		Data:    text,
		IsError: true,
	})
}

// Buffer starts accumulating content instead of immediate output.
func (f *JSONFormatter) Buffer() {
	f.buffering = true
}

// IsBuffering returns true if the formatter is in buffered mode.
func (f *JSONFormatter) IsBuffering() bool {
	return f.buffering
}

// Flush writes all buffered content and returns to immediate mode.
func (f *JSONFormatter) Flush() error {
	if !f.buffering || len(f.bufferList) == 0 {
		return nil
	}

	// In JSON format, we can output an array of all buffered items
	wrapper := struct {
		BatchOutput []JSONOutput `json:"batchOutput"`
		Timestamp   string       `json:"timestamp"`
	}{
		BatchOutput: f.bufferList,
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	bytes, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling JSON batch: %v", err)
	}

	fmt.Fprintln(f.stdout, string(bytes))

	// Clear buffer and reset buffering state
	f.bufferList = make([]JSONOutput, 0)
	f.buffering = false

	return nil
}
