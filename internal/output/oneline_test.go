// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestOneLineFormatter(t *testing.T) {
	var stdout, stderr bytes.Buffer

	formatter := newOneLineFormatter(Config{
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

	// Verify outputs
	stdoutLines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	stderrLines := strings.Split(strings.TrimSpace(stderr.String()), "\n")

	if len(stdoutLines) != 2 {
		t.Errorf("Expected 2 lines in stdout, got %d: %v", len(stdoutLines), stdoutLines)
	}

	if len(stderrLines) != 1 {
		t.Errorf("Expected 1 line in stderr, got %d: %v", len(stderrLines), stderrLines)
	}

	// Check that each line follows the format: timestamp,type,level,metadata...,data
	for _, line := range append(stdoutLines, stderrLines...) {
		parts := strings.Split(line, ",")

		if len(parts) < 4 {
			t.Errorf("Line has fewer parts than expected: %s", line)
			continue
		}

		// Check timestamp format (RFC3339)
		if len(parts[0]) < 20 {
			t.Errorf("First part doesn't look like a timestamp: %s", parts[0])
		}

		// Check second part is content type
		if parts[1] != string(TypeGeneral) && parts[1] != string(TypeDeviceList) {
			t.Errorf("Unexpected content type: %s", parts[1])
		}

		// Check third part is INFO or ERROR
		if parts[2] != "INFO" && parts[2] != "ERROR" {
			t.Errorf("Expected INFO or ERROR, got: %s", parts[2])
		}
	}

	// Test buffering functionality
	var bufStdout, bufStderr bytes.Buffer
	bufferFormatter := newOneLineFormatter(Config{
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
		Data: "Buffered message",
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
}
