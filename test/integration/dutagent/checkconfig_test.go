// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dutagent_integration contains integration tests for the dutagent binary.
package dutagent_integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMain(m *testing.M) {
	bin, err := buildDutagent()
	if err != nil {
		os.Stderr.WriteString("failed to build dutagent: " + err.Error() + "\n")
		os.Exit(1)
	}

	os.Setenv("DUTAGENT_TEST_BINARY", bin)

	code := m.Run()
	os.Remove(bin)
	os.Exit(code)
}

func dutagentBinary(t *testing.T) string {
	t.Helper()

	bin := os.Getenv("DUTAGENT_TEST_BINARY")
	if bin == "" {
		t.Fatal("DUTAGENT_TEST_BINARY not set — was TestMain skipped?")
	}

	return bin
}

func buildDutagent() (string, error) {
	bin, err := os.CreateTemp("", "dutagent-test-*")
	if err != nil {
		return "", err
	}

	bin.Close()

	cmd := exec.Command("go", "build", "-o", bin.Name(), "../../../cmds/dutagent")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", err
	}

	return bin.Name(), nil
}

func exampleConfigs(t *testing.T) []string {
	t.Helper()

	// Resolve relative to this source file so the test works regardless of
	// working directory (e.g. go test ./...).
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")

	configs, err := filepath.Glob(filepath.Join(root, "pkg", "module", "*", "*-example-cfg.yml"))
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}

	if len(configs) == 0 {
		t.Fatal("no example config files found — check the glob pattern")
	}

	return configs
}

// TestCheckConfigExampleFiles runs dutagent --check-config against every example
// config file shipped with the module packages and asserts:
//   - exit code 0
//   - "Configuration is valid" in output
func TestCheckConfigExampleFiles(t *testing.T) {
	bin := dutagentBinary(t)

	for _, cfgPath := range exampleConfigs(t) {
		t.Run(filepath.Base(filepath.Dir(cfgPath)), func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), bin, "--check-config", "-c", cfgPath)
			out, err := cmd.CombinedOutput()

			if err != nil {
				t.Errorf("unexpected exit: %v\n%s", err, out)
				return
			}

			if !bytes.Contains(out, []byte("Configuration is valid")) {
				t.Errorf("expected 'Configuration is valid' in output, got:\n%s", out)
			}
		})
	}
}
