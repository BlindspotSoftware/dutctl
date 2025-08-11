// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteMetadata(t *testing.T) {
	tests := []struct {
		name       string
		verbose    bool
		metadata   map[string]string
		wantOutput string
	}{
		{
			name:       "no metadata",
			verbose:    true,
			metadata:   map[string]string{},
			wantOutput: "",
		},
		{
			name:       "not verbose",
			verbose:    false,
			metadata:   map[string]string{"server": "localhost:1024", "msg": "test message"},
			wantOutput: "",
		},
		{
			name:    "server only",
			verbose: true,
			metadata: map[string]string{
				"server": "localhost:1024",
			},
			wantOutput: "connected to localhost:1024\n\n",
		},
		{
			name:    "message only",
			verbose: true,
			metadata: map[string]string{
				"msg": "test message",
			},
			wantOutput: "(test message)\n\n",
		},
		{
			name:    "device only",
			verbose: true,
			metadata: map[string]string{
				"device": "device1",
			},
			wantOutput: "device \"device1\"\n\n",
		},
		{
			name:    "command only",
			verbose: true,
			metadata: map[string]string{
				"command": "status",
			},
			wantOutput: "executing 'status'\n\n",
		},
		{
			name:    "command with args",
			verbose: true,
			metadata: map[string]string{
				"command": "flash",
				"args":    "firmware.bin",
			},
			wantOutput: "executing 'flash firmware.bin'\n\n",
		},
		{
			name:    "device and command",
			verbose: true,
			metadata: map[string]string{
				"device":  "device1",
				"command": "power",
			},
			wantOutput: "device \"device1\" executing 'power'\n\n",
		},
		{
			name:    "device with command",
			verbose: true,
			metadata: map[string]string{
				"device":  "device1",
				"command": "status",
			},
			wantOutput: "device \"device1\" executing 'status'\n\n",
		},
		{
			name:    "device with command and args",
			verbose: true,
			metadata: map[string]string{
				"device":  "device1",
				"command": "exec",
				"args":    "--verbose",
			},
			wantOutput: "device \"device1\" executing 'exec --verbose'\n\n",
		},
		{
			name:    "server with device and command",
			verbose: true,
			metadata: map[string]string{
				"server":  "localhost:1024",
				"device":  "device1",
				"command": "status",
			},
			wantOutput: "connected to localhost:1024 device \"device1\" executing 'status'\n\n",
		},
		{
			name:    "full known key sentence",
			verbose: true,
			metadata: map[string]string{
				"server":  "localhost:1024",
				"msg":     "test message",
				"device":  "device1",
				"command": "exec",
				"args":    "--verbose",
			},
			wantOutput: "connected to localhost:1024 (test message) device \"device1\" executing 'exec --verbose'\n\n",
		},
		{
			name:    "only other keys",
			verbose: true,
			metadata: map[string]string{
				"key1": "value1",
				"key2": "value2",
				"key3": "value3",
			},
			wantOutput: "key1: value1\nkey2: value2\nkey3: value3\n\n",
		},
		{
			name:    "mixed known and other keys",
			verbose: true,
			metadata: map[string]string{
				"server":  "localhost:1024",
				"device":  "device1",
				"command": "exec",
				"key1":    "value1",
				"key2":    "value2",
				"key3":    "value3",
			},
			wantOutput: "connected to localhost:1024 device \"device1\" executing 'exec'\nkey1: value1\nkey2: value2\nkey3: value3\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			formatter := &TextFormatter{
				stdout:        &buf,
				stderr:        &buf, // Using same buffer for simplicity
				verbose:       tt.verbose,
				metadataCache: make(map[string]string), // Initialize the metadata cache map
			}

			content := Content{
				Type:     TypeGeneral,
				Data:     "test data",
				Metadata: tt.metadata,
			}

			formatter.writeMetadata(content, &buf)

			if got := buf.String(); got != tt.wantOutput {
				t.Errorf("writeMetadata() = %q, want %q", got, tt.wantOutput)
			}
		})
	}
}

// TestMetadataCaching tests the metadata caching mechanism for TextFormatter
// This test verifies that:
// 1. Metadata is printed on first write
// 2. Same metadata is not printed on subsequent writes to the same writer
// 3. Metadata is printed again when it changes
// 4. Metadata is printed when switching between stdout and stderr
// 5. Metadata is printed after cache is cleared
func TestMetadataCaching(t *testing.T) {
	// Create buffers for stdout and stderr
	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	// Create formatter with our buffers
	formatter := newTextFormatter(Config{
		Stdout:  stdoutBuf,
		Stderr:  stderrBuf,
		Verbose: true,
	})

	// Test case 1: First write includes metadata
	content1 := Content{
		Metadata: map[string]string{
			"server":  "test-server",
			"device":  "test-device",
			"command": "test-command",
		},
		Data:    "First message",
		Type:    TypeGeneral,
		IsError: false,
	}
	formatter.WriteContent(content1)
	firstOutput := stdoutBuf.String()

	// Test case 2: Second write with same metadata does not include metadata
	content2 := Content{
		Metadata: map[string]string{
			"server":  "test-server",
			"device":  "test-device",
			"command": "test-command",
		},
		Data:    "Second message with same metadata",
		Type:    TypeGeneral,
		IsError: false,
	}
	formatter.WriteContent(content2)
	secondOutput := stdoutBuf.String()[len(firstOutput):] // Get just the new content

	// Test case 3: Third write with different metadata includes new metadata
	content3 := Content{
		Metadata: map[string]string{
			"server":  "different-server",
			"device":  "test-device",
			"command": "test-command",
		},
		Data:    "Third message with different metadata",
		Type:    TypeGeneral,
		IsError: false,
	}
	formatter.WriteContent(content3)
	thirdOutput := stdoutBuf.String()[len(firstOutput)+len(secondOutput):] // Get just the newest content

	// Test case 4: Writing to stderr includes metadata even with same content
	errorContent := Content{
		Metadata: map[string]string{
			"server":  "different-server",
			"device":  "test-device",
			"command": "test-command",
		},
		Data:    "Error message",
		Type:    TypeGeneral,
		IsError: true, // This will make it write to stderr
	}
	formatter.WriteContent(errorContent)
	stderrOutput := stderrBuf.String()

	// Test case 5: Back to stdout also includes metadata because writer changed
	stdoutAgainContent := Content{
		Metadata: map[string]string{
			"server":  "different-server",
			"device":  "test-device",
			"command": "test-command",
		},
		Data:    "Back to stdout",
		Type:    TypeGeneral,
		IsError: false,
	}
	formatter.WriteContent(stdoutAgainContent)
	fourthOutput := stdoutBuf.String()[len(firstOutput)+len(secondOutput)+len(thirdOutput):] // Get newest content

	// Test case 6: Clearing cache causes metadata to be printed again
	formatter.clearMetadataCache()

	clearCacheContent := Content{
		Metadata: map[string]string{
			"server":  "different-server",
			"device":  "test-device",
			"command": "test-command",
		},
		Data:    "After cache clear",
		Type:    TypeGeneral,
		IsError: false,
	}
	formatter.WriteContent(clearCacheContent)
	fifthOutput := stdoutBuf.String()[len(firstOutput)+len(secondOutput)+len(thirdOutput)+len(fourthOutput):] // Get newest content

	// Verify test case 1: First output contains metadata
	if !strings.Contains(firstOutput, "test-server") || !strings.Contains(firstOutput, "test-device") || !strings.Contains(firstOutput, "test-command") {
		t.Errorf("First output should contain metadata. Got: %q", firstOutput)
	}

	// Verify test case 2: Second output does NOT contain metadata (cached)
	if strings.Contains(secondOutput, "test-server") || strings.Contains(secondOutput, "test-device") || strings.Contains(secondOutput, "test-command") {
		t.Errorf("Second output should NOT contain metadata (should be cached). Got: %q", secondOutput)
	} else if !strings.Contains(secondOutput, "Second message") {
		t.Errorf("Second output missing message content. Got: %q", secondOutput)
	}

	// Verify test case 3: Third output contains changed metadata
	if !strings.Contains(thirdOutput, "different-server") {
		t.Errorf("Third output missing expected changed metadata. Got: %q", thirdOutput)
	}

	// Verify test case 4: Stderr output includes metadata
	if !strings.Contains(stderrOutput, "different-server") {
		t.Errorf("Stderr output missing expected metadata. Got: %q", stderrOutput)
	}

	// Verify test case 5: Fourth output includes metadata due to writer change
	if !strings.Contains(fourthOutput, "different-server") {
		t.Errorf("Fourth output should include metadata (due to writer change). Got: %q", fourthOutput)
	}

	// Verify test case 6: Fifth output includes metadata due to cache clear
	if !strings.Contains(fifthOutput, "different-server") {
		t.Errorf("Fifth output should include metadata (due to cache clear). Got: %q", fifthOutput)
	}
}
