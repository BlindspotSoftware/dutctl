// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flashemulate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
)

func TestInit(t *testing.T) {
	tests := []struct {
		name        string
		flashEmulate FlashEmulate
		expectErr   bool
	}{
		{
			name:        "valid config",
			flashEmulate: FlashEmulate{Tool: "/bin/sh", Chip: "N25Q256A13"},
			expectErr:   false,
		},
		{
			name:        "missing chip",
			flashEmulate: FlashEmulate{Tool: "/bin/sh"},
			expectErr:   true,
		},
		{
			name:        "tool not found",
			flashEmulate: FlashEmulate{Tool: "this-tool-does-not-exist-xyzabc123", Chip: "N25Q256A13"},
			expectErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.flashEmulate.Init()
			if (err != nil) != tt.expectErr {
				t.Errorf("unexpected error state (wantErr=%v): %v", tt.expectErr, err)
			}
		})
	}
}

func TestInitDefaultTool(t *testing.T) {
	// Create a fake em100 binary in a temp dir so the test is hermetic.
	dir := t.TempDir()
	fakeTool := filepath.Join(dir, defaultTool)

	if err := os.WriteFile(fakeTool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	e := FlashEmulate{Chip: "N25Q256A13"}

	if err := e.Init(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if e.Tool != defaultTool {
		t.Errorf("expected Tool to be defaulted to %q, got %q", defaultTool, e.Tool)
	}
}

func TestDeinit(t *testing.T) {
	t.Run("no image to clean up", func(t *testing.T) {
		e := FlashEmulate{}
		if err := e.Deinit(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("removes image file on cleanup", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "flash-emulate-test-*")
		if err != nil {
			t.Fatal(err)
		}

		f.Close()

		e := FlashEmulate{localImagePath: f.Name()}
		if err := e.Deinit(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		if _, statErr := os.Stat(f.Name()); !os.IsNotExist(statErr) {
			t.Errorf("expected file %q to be removed after Deinit", f.Name())
		}
	})
}

func TestRun(t *testing.T) {
	tests := []struct {
		name        string
		flashEmulate FlashEmulate
		args        []string
		expectErr   bool
	}{
		{
			name:        "missing image argument",
			flashEmulate: FlashEmulate{Tool: "/bin/sh", Chip: "N25Q256A13"},
			args:        []string{},
			expectErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sesh := &mock.Session{}
			ctx := context.Background()

			err := tt.flashEmulate.Run(ctx, sesh, tt.args...)
			if (err != nil) != tt.expectErr {
				t.Errorf("unexpected error state (wantErr=%v): %v", tt.expectErr, err)
			}
		})
	}

	t.Run("successful run uploads image and invokes tool", func(t *testing.T) {
		dir := t.TempDir()
		fakeTool := filepath.Join(dir, "em100")

		if err := os.WriteFile(fakeTool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		sesh := &mock.Session{
			RequestedFileResponse: strings.NewReader("fake firmware content"),
		}

		e := FlashEmulate{Tool: fakeTool, Chip: "N25Q256A13"}

		if err := e.Run(context.Background(), sesh, "firmware.rom"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		if !sesh.RequestFileCalled {
			t.Error("expected RequestFile to be called")
		}

		if sesh.RequestedFileName != "firmware.rom" {
			t.Errorf("expected requested file name %q, got %q", "firmware.rom", sesh.RequestedFileName)
		}

		if err := e.Deinit(); err != nil {
			t.Errorf("unexpected Deinit error: %v", err)
		}
	})

	t.Run("uploadImage failure cleans up temp file", func(t *testing.T) {
		sesh := &mock.Session{
			RequestFileErr: errors.New("fake transfer error"),
		}

		e := FlashEmulate{Tool: "/bin/sh", Chip: "N25Q256A13"}

		if err := e.Run(context.Background(), sesh, "firmware.rom"); err == nil {
			t.Error("expected error but got nil")
		}

		if e.localImagePath != "" {
			t.Errorf("expected localImagePath to be empty after failure, got %q", e.localImagePath)
		}
	})

	t.Run("execute failure cleans up temp file", func(t *testing.T) {
		dir := t.TempDir()
		fakeTool := filepath.Join(dir, "em100")

		if err := os.WriteFile(fakeTool, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		sesh := &mock.Session{
			RequestedFileResponse: strings.NewReader("fake firmware"),
		}

		e := FlashEmulate{Tool: fakeTool, Chip: "N25Q256A13"}

		if err := e.Run(context.Background(), sesh, "firmware.rom"); err == nil {
			t.Error("expected error but got nil")
		}

		if e.localImagePath != "" {
			t.Errorf("expected localImagePath to be empty after failure, got %q", e.localImagePath)
		}
	})

	t.Run("second run cleans up temp file from first run", func(t *testing.T) {
		dir := t.TempDir()
		fakeTool := filepath.Join(dir, "em100")

		if err := os.WriteFile(fakeTool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		e := FlashEmulate{Tool: fakeTool, Chip: "N25Q256A13"}
		ctx := context.Background()

		sesh1 := &mock.Session{RequestedFileResponse: strings.NewReader("firmware v1")}
		if err := e.Run(ctx, sesh1, "fw1.rom"); err != nil {
			t.Fatalf("first Run failed: %v", err)
		}

		firstPath := e.localImagePath

		sesh2 := &mock.Session{RequestedFileResponse: strings.NewReader("firmware v2")}
		if err := e.Run(ctx, sesh2, "fw2.rom"); err != nil {
			t.Fatalf("second Run failed: %v", err)
		}

		if _, statErr := os.Stat(firstPath); !os.IsNotExist(statErr) {
			t.Errorf("expected first temp file %q to be cleaned up before second run", firstPath)
		}

		if err := e.Deinit(); err != nil {
			t.Errorf("unexpected Deinit error: %v", err)
		}
	})
}

func TestHelp(t *testing.T) {
	tests := []struct {
		name          string
		flashEmulate  FlashEmulate
		expectStrings []string
	}{
		{
			name:         "contains chip and tool",
			flashEmulate: FlashEmulate{Tool: "em100", Chip: "N25Q256A13"},
			expectStrings: []string{
				"em100",
				"N25Q256A13",
				"image",
			},
		},
		{
			name:         "contains device number when set",
			flashEmulate: FlashEmulate{Tool: "em100", Chip: "N25Q256A13", Device: "2"},
			expectStrings: []string{
				"em100",
				"N25Q256A13",
				"2",
			},
		},
		{
			name:         "reflects custom chip name",
			flashEmulate: FlashEmulate{Tool: "em100", Chip: "W25Q128JV"},
			expectStrings: []string{
				"W25Q128JV",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helpStr := tt.flashEmulate.Help()
			for _, s := range tt.expectStrings {
				if !strings.Contains(helpStr, s) {
					t.Errorf("expected %q to appear in Help output:\n%s", s, helpStr)
				}
			}
		})
	}
}

func TestCmdline(t *testing.T) {
	tests := []struct {
		name        string
		flashEmulate FlashEmulate
		expected    []string
	}{
		{
			name: "basic cmdline without device",
			flashEmulate: FlashEmulate{
				Chip:           "N25Q256A13",
				localImagePath: "./flash-emulate-image",
			},
			expected: []string{"--stop", "--set", "N25Q256A13", "-d", "./flash-emulate-image", "-v", "--start"},
		},
		{
			name: "cmdline with device selector",
			flashEmulate: FlashEmulate{
				Chip:           "N25Q256A13",
				Device:         "1",
				localImagePath: "./flash-emulate-image",
			},
			expected: []string{"--device", "1", "--stop", "--set", "N25Q256A13", "-d", "./flash-emulate-image", "-v", "--start"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.flashEmulate.cmdline()

			if len(got) != len(tt.expected) {
				t.Fatalf("cmdline length: expected %d args %v, got %d args %v", len(tt.expected), tt.expected, len(got), got)
			}

			for i := range tt.expected {
				if got[i] != tt.expected[i] {
					t.Errorf("arg[%d]: expected %q, got %q", i, tt.expected[i], got[i])
				}
			}
		})
	}
}
