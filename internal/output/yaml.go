// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package output

import (
	"fmt"
	"io"
	"log"
	"time"

	"gopkg.in/yaml.v3"
)

// YAMLFormatter formats output as YAML documents.
type YAMLFormatter struct {
	stdout     io.Writer
	stderr     io.Writer
	verbose    bool
	buffering  bool
	bufferList []YAMLOutput
}

// YAMLOutput is a struct for YAML formatted output.
type YAMLOutput struct {
	ContentType string            `yaml:"contentType"`
	Data        interface{}       `yaml:"data"`
	Error       bool              `yaml:"error,omitempty"`
	Metadata    map[string]string `yaml:"metadata,omitempty"`
	Timestamp   string            `yaml:"timestamp"`
}

// newYAMLFormatter creates a new YAML formatter.
func newYAMLFormatter(config Config) *YAMLFormatter {
	return &YAMLFormatter{
		stdout:     config.Stdout,
		stderr:     config.Stderr,
		verbose:    config.Verbose,
		buffering:  false,
		bufferList: make([]YAMLOutput, 0),
	}
}

// WriteContent formats and outputs structured content.
func (f *YAMLFormatter) WriteContent(content Content) {
	output := YAMLOutput{
		ContentType: string(content.Type),
		Data:        content.Data,
		Error:       content.IsError,
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	if f.verbose && len(content.Metadata) > 0 {
		output.Metadata = content.Metadata
	}

	if f.buffering {
		f.bufferList = append(f.bufferList, output)

		return
	}

	writer := f.stdout
	if content.IsError {
		writer = f.stderr
	}

	bytes, err := yaml.Marshal(output)
	if err != nil {
		log.Printf("Error marshaling YAML: %v", err)

		return
	}

	fmt.Fprintf(writer, "---\n%s\n", string(bytes))
}

// Write sends text to standard output.
func (f *YAMLFormatter) Write(text string) {
	f.WriteContent(Content{
		Type: TypeGeneral,
		Data: text,
	})
}

// WriteErr sends text to standard error.
func (f *YAMLFormatter) WriteErr(text string) {
	f.WriteContent(Content{
		Type:    TypeGeneral,
		Data:    text,
		IsError: true,
	})
}

// Buffer starts accumulating content instead of immediate output.
func (f *YAMLFormatter) Buffer() {
	f.buffering = true
}

// IsBuffering returns whether the formatter is currently buffering output.
func (f *YAMLFormatter) IsBuffering() bool {
	return f.buffering
}

// output writes a slice of YAML outputs to the specified writer.
func output(outputs []YAMLOutput, writer io.Writer) {
	for _, output := range outputs {
		bytes, err := yaml.Marshal(output)
		if err != nil {
			log.Printf("Error marshaling YAML during flush: %v", err)

			continue
		}

		fmt.Fprintf(writer, "---\n%s\n", string(bytes))
	}
}

// groupOutputsByStream separates outputs into stdout and stderr groups based on Error field.
func groupByStream(bufferList []YAMLOutput) ([]YAMLOutput, []YAMLOutput) {
	var stdoutOutputs, stderrOutputs []YAMLOutput

	for _, output := range bufferList {
		if output.Error {
			stderrOutputs = append(stderrOutputs, output)
		} else {
			stdoutOutputs = append(stdoutOutputs, output)
		}
	}

	return stdoutOutputs, stderrOutputs
}

// Flush ensures all output is written.
func (f *YAMLFormatter) Flush() error {
	if !f.buffering || len(f.bufferList) == 0 {
		return nil
	}

	stdoutOutputs, stderrOutputs := groupByStream(f.bufferList)

	// Write stdout outputs
	if len(stdoutOutputs) > 0 {
		output(stdoutOutputs, f.stdout)
	}

	// Write stderr outputs
	if len(stderrOutputs) > 0 {
		output(stderrOutputs, f.stderr)
	}

	// Clear the buffer
	f.bufferList = f.bufferList[:0]
	// Reset buffering flag
	f.buffering = false

	return nil
}
