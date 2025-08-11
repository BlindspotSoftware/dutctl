// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONFormatter(t *testing.T) {
	var stdout, stderr bytes.Buffer

	formatter := newJSONFormatter(Config{
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

	// Verify outputs - parse JSON to verify structure
	stdoutData := stdout.String()
	stderrData := stderr.String()

	// Each JSON object is a complete JSON document, we need to split them correctly
	stdoutLines := strings.Split(strings.TrimSpace(stdoutData), "\n")
	var stdoutBlocks []string
	var currentBlock strings.Builder

	for _, line := range stdoutLines {
		currentBlock.WriteString(line)
		if line == "}" {
			stdoutBlocks = append(stdoutBlocks, currentBlock.String())
			currentBlock.Reset()
		} else {
			currentBlock.WriteString("\n")
		}
	}

	if len(stdoutBlocks) != 2 {
		t.Errorf("Expected 2 JSON blocks in stdout, got %d", len(stdoutBlocks))
	}

	// Should have 1 JSON object in stderr
	stderrLines := strings.Split(strings.TrimSpace(stderrData), "\n")
	var stderrBlocks []string
	currentBlock.Reset()

	for _, line := range stderrLines {
		currentBlock.WriteString(line)
		if line == "}" {
			stderrBlocks = append(stderrBlocks, currentBlock.String())
			currentBlock.Reset()
		} else {
			currentBlock.WriteString("\n")
		}
	}

	if len(stderrBlocks) != 1 {
		t.Errorf("Expected 1 JSON block in stderr, got %d", len(stderrBlocks))
	}

	// Parse and validate each JSON object
	for i, block := range stdoutBlocks {
		var output JSONOutput
		if err := json.Unmarshal([]byte(block), &output); err != nil {
			t.Errorf("Failed to parse JSON from stdout block %d: %v", i, err)
			continue
		}

		// Validate the JSON structure
		validateJSONOutput(t, output, false)
	}

	for i, block := range stderrBlocks {
		var output JSONOutput
		if err := json.Unmarshal([]byte(block), &output); err != nil {
			t.Errorf("Failed to parse JSON from stderr block %d: %v", i, err)
			continue
		}

		// Validate the JSON structure
		validateJSONOutput(t, output, true)

		// Specifically check that error is true
		if !output.Error {
			t.Errorf("Expected Error to be true for stderr output")
		}
	}

	// Test metadata inclusion based on verbose flag
	var verboseStdout, nonVerboseStdout bytes.Buffer

	verboseFormatter := newJSONFormatter(Config{
		Stdout:  &verboseStdout,
		Stderr:  &stderr,
		Verbose: true,
	})

	nonVerboseFormatter := newJSONFormatter(Config{
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

	var verboseOutput, nonVerboseOutput JSONOutput

	if err := json.Unmarshal([]byte(strings.TrimSpace(verboseStdout.String())), &verboseOutput); err != nil {
		t.Errorf("Failed to parse verbose JSON output: %v", err)
	} else {
		if verboseOutput.Metadata == nil {
			t.Errorf("Verbose output should include metadata")
		} else if len(verboseOutput.Metadata) != 2 {
			t.Errorf("Expected 2 metadata entries, got %d", len(verboseOutput.Metadata))
		}
	}

	if err := json.Unmarshal([]byte(strings.TrimSpace(nonVerboseStdout.String())), &nonVerboseOutput); err != nil {
		t.Errorf("Failed to parse non-verbose JSON output: %v", err)
	} else {
		if len(nonVerboseOutput.Metadata) > 0 {
			t.Errorf("Non-verbose output should not include metadata")
		}
	}

	// Test buffering functionality
	var bufStdout, bufStderr bytes.Buffer
	bufferFormatter := newJSONFormatter(Config{
		Stdout:  &bufStdout,
		Stderr:  &bufStderr,
		Verbose: true,
	})

	bufferFormatter.Buffer()
	if !bufferFormatter.IsBuffering() {
		t.Error("Formatter should be in buffering mode")
	}

	bufferFormatter.WriteContent(Content{
		Type: TypeGeneral,
		Data: "Buffered message 1",
	})

	bufferFormatter.WriteContent(Content{
		Type: TypeGeneral,
		Data: "Buffered message 2",
	})

	if bufStdout.Len() != 0 {
		t.Error("Nothing should be written to stdout while buffering")
	}

	err := bufferFormatter.Flush()
	if err != nil {
		t.Errorf("Flush returned error: %v", err)
	}

	if bufStdout.Len() == 0 {
		t.Error("Buffer should have been written to stdout after Flush")
	}

	if bufferFormatter.IsBuffering() {
		t.Error("Formatter should no longer be in buffering mode after Flush")
	}

	// Parse the batch output to verify it has the correct structure
	var batchOutput struct {
		BatchOutput []JSONOutput `json:"batchOutput"`
		Timestamp   string       `json:"timestamp"`
	}

	if err := json.Unmarshal([]byte(strings.TrimSpace(bufStdout.String())), &batchOutput); err != nil {
		t.Errorf("Failed to parse batch output JSON: %v", err)
	} else {
		if len(batchOutput.BatchOutput) != 2 {
			t.Errorf("Expected 2 items in batch output, got %d", len(batchOutput.BatchOutput))
		}
		if batchOutput.Timestamp == "" {
			t.Error("Batch output timestamp should not be empty")
		}
	}
}

// Helper function to validate JSON output structure
func validateJSONOutput(t *testing.T, output JSONOutput, isError bool) {
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
