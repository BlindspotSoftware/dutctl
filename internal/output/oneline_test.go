// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestDeviceEntryString(t *testing.T) {
	tests := []struct {
		name string
		data DeviceEntry
		want string
	}{
		{
			name: "free",
			data: DeviceEntry{Name: "board1"},
			want: "board1",
		},
		{
			name: "in use without expiry",
			data: DeviceEntry{Name: "board2", Locked: true, Owner: "bob@host"},
			want: "board2=in-use:bob@host",
		},
		{
			name: "explicitly locked",
			data: DeviceEntry{Name: "board3", Locked: true, Owner: "alice@host", ExpiresAt: 1784500000},
			want: "board3=locked:alice@host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deviceEntryString(tt.data); got != tt.want {
				t.Errorf("deviceEntryString(%+v) = %q, want %q", tt.data, got, tt.want)
			}
		})
	}
}

func TestOneLineFormatterKeepsAContentOnOneLine(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{"line ending dropped", "The board is off.\n", `"The board is off."`},
		{"CRLF line ending dropped", "U-Boot\r\n", "U-Boot"},
		{"inner lines escaped", "ARGUMENTS:\n\ton\n", `ARGUMENTS:\n` + "\t" + `on`},
		{"carriage return escaped", "50%\r100%", `50%\r100%`},
		{"only one line ending dropped", "end\n\n", `end\n`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer

			newOneLineFormatter(Config{Stdout: &stdout}).Write(tt.data)

			line, found := strings.CutSuffix(stdout.String(), "\n")
			if !found || strings.Contains(line, "\n") {
				t.Fatalf("output %q is not one line", stdout.String())
			}

			if data := line[strings.LastIndex(line, ",INFO,")+len(",INFO,"):]; data != tt.want {
				t.Errorf("data column %s, want %s", data, tt.want)
			}
		})
	}
}

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
		Data: []DeviceEntry{{Name: "device1"}, {Name: "device2"}, {Name: "device3"}},
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

	bufferFormatter.Flush()

	if bufStdout.Len() == 0 {
		t.Error("Buffer should have been written to stdout after Flush")
	}

	if bufferFormatter.IsBuffering() {
		t.Error("Formatter should no longer be in buffering mode after Flush")
	}
}
