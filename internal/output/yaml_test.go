// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package output

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestYAMLFormatter(t *testing.T) {
	var stdout, stderr bytes.Buffer

	formatter := newYAMLFormatter(Config{
		Stdout:  &stdout,
		Stderr:  &stderr,
		Verbose: true,
	})

	// Test case 1: Simple string output
	formatter.WriteContent(Content{
		Type: TypeGeneral,
		Data: "Simple message",
	})

	// Test case 2: Output with metadata
	formatter.WriteContent(Content{
		Type: TypeDeviceList,
		Data: []string{"device1", "device2", "device3"},
		Metadata: map[string]string{
			"server": "localhost:1024",
			"device": "test-device",
		},
	})

	// Test case 3: Error output
	formatter.WriteContent(Content{
		Type:    TypeGeneral,
		Data:    "Error message",
		IsError: true,
		Metadata: map[string]string{
			"command": "status",
			"error":   "connection failed",
		},
	})

	// Verify outputs - parse YAML to verify structure
	stdoutData := stdout.String()
	stderrData := stderr.String()

	// YAML documents are separated by "---"
	// Split by "---" but keep in mind the first element might be empty
	stdoutDocs := strings.Split(stdoutData, "---")
	stderrDocs := strings.Split(stderrData, "---")

	// Filter out empty documents (first one is often empty)
	stdoutDocs = filterNonEmptyDocs(stdoutDocs)
	stderrDocs = filterNonEmptyDocs(stderrDocs)

	if len(stdoutDocs) != 2 {
		t.Errorf("Expected 2 YAML documents in stdout, got %d", len(stdoutDocs))
	}

	if len(stderrDocs) != 1 {
		t.Errorf("Expected 1 YAML document in stderr, got %d", len(stderrDocs))
	}

	// Parse and validate each YAML document
	for i, doc := range stdoutDocs {
		var output YAMLOutput
		if err := yaml.Unmarshal([]byte(doc), &output); err != nil {
			t.Errorf("Failed to parse YAML from stdout document %d: %v", i, err)
			continue
		}

		// Validate the YAML structure
		validateYAMLOutput(t, output, false)
	}

	for i, doc := range stderrDocs {
		var output YAMLOutput
		if err := yaml.Unmarshal([]byte(doc), &output); err != nil {
			t.Errorf("Failed to parse YAML from stderr document %d: %v", i, err)
			continue
		}

		// Validate the YAML structure
		validateYAMLOutput(t, output, true)

		// Specifically check that error is true
		if !output.Error {
			t.Errorf("Expected Error to be true for stderr output")
		}
	}

	// Test metadata inclusion based on verbose flag
	var verboseStdout, nonVerboseStdout bytes.Buffer

	verboseFormatter := newYAMLFormatter(Config{
		Stdout:  &verboseStdout,
		Stderr:  &stderr,
		Verbose: true,
	})

	nonVerboseFormatter := newYAMLFormatter(Config{
		Stdout:  &nonVerboseStdout,
		Stderr:  &stderr,
		Verbose: false,
	})

	testMetadata := map[string]string{
		"key1": "value1",
		"key2": "value2",
	}

	verboseFormatter.WriteContent(Content{
		Type:     TypeGeneral,
		Data:     "Test message",
		Metadata: testMetadata,
	})

	nonVerboseFormatter.WriteContent(Content{
		Type:     TypeGeneral,
		Data:     "Test message",
		Metadata: testMetadata,
	})

	verboseOutput := parseYAMLDocument(t, strings.TrimPrefix(verboseStdout.String(), "---\n"))
	nonVerboseOutput := parseYAMLDocument(t, strings.TrimPrefix(nonVerboseStdout.String(), "---\n"))

	if verboseOutput.Metadata == nil {
		t.Errorf("Verbose output should include metadata")
	} else if len(verboseOutput.Metadata) != 2 {
		t.Errorf("Expected 2 metadata entries, got %d", len(verboseOutput.Metadata))
	}

	if len(nonVerboseOutput.Metadata) > 0 {
		t.Errorf("Non-verbose output should not include metadata")
	}

	// Test buffering functionality
	var bufStdout, bufStderr bytes.Buffer
	bufferFormatter := newYAMLFormatter(Config{
		Stdout:  &bufStdout,
		Stderr:  &bufStderr,
		Verbose: true,
	})

	bufferFormatter.Buffer()
	if !bufferFormatter.IsBuffering() {
		t.Error("Formatter should be in buffering mode")
	}

	// Write to both stdout and stderr buffers
	bufferFormatter.WriteContent(Content{
		Type: TypeGeneral,
		Data: "Buffered stdout message",
	})

	bufferFormatter.WriteContent(Content{
		Type:    TypeGeneral,
		Data:    "Buffered stderr message",
		IsError: true,
	})

	if bufStdout.Len() != 0 || bufStderr.Len() != 0 {
		t.Error("Nothing should be written to output while buffering")
	}

	err := bufferFormatter.Flush()
	if err != nil {
		t.Errorf("Flush returned error: %v", err)
	}

	if bufStdout.Len() == 0 {
		t.Error("Buffer should have been written to stdout after Flush")
	}

	if bufStderr.Len() == 0 {
		t.Error("Buffer should have been written to stderr after Flush")
	}

	if bufferFormatter.IsBuffering() {
		t.Error("Formatter should no longer be in buffering mode after Flush")
	}
}

// Helper function to validate YAML output structure
func validateYAMLOutput(t *testing.T, output YAMLOutput, isError bool) {
	t.Helper()

	if output.ContentType == "" {
		t.Error("ContentType should not be empty")
	}

	if output.Timestamp == "" {
		t.Error("Timestamp should not be empty")
	}

	if output.Error != isError {
		t.Errorf("Error field should be %v", isError)
	}

	// Data should exist, but its type varies so we can't check much else
	if output.Data == nil {
		t.Error("Data should not be nil")
	}
}

// Filter out empty documents from YAML split
func filterNonEmptyDocs(docs []string) []string {
	var result []string
	for _, doc := range docs {
		trimmed := strings.TrimSpace(doc)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// Parse a YAML document into a YAMLOutput struct
func parseYAMLDocument(t *testing.T, doc string) YAMLOutput {
	t.Helper()
	var output YAMLOutput
	if err := yaml.Unmarshal([]byte(doc), &output); err != nil {
		t.Fatalf("Failed to parse YAML document: %v", err)
	}
	return output
}
