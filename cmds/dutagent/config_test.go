// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"gopkg.in/yaml.v3"
)

// Tests for the full config struct YAML decode path (version + devices wrapper).
// Devlist/Device/Command/Module parsing should be tested in the respective packages.

func loadTestdata(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("failed to read testdata/%s: %v", name, err)
	}

	return data
}

func TestValidConfig(t *testing.T) {
	data := loadTestdata(t, "valid_config.yaml")

	var cfg config

	err := yaml.Unmarshal(data, &cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Devices) != 1 {
		t.Errorf("device count: want 1, got %d", len(cfg.Devices))
	}
}

func TestInvalidConfigEmptyDevices(t *testing.T) {
	data := loadTestdata(t, "invalid_config_empty_devices.yaml")

	var cfg config

	err := yaml.Unmarshal(data, &cfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	t.Logf("error message: %s", err)

	if !errors.Is(err, dut.ErrEmptyDevices) {
		t.Errorf("errors.Is: want %v, got %v", dut.ErrEmptyDevices, err)
	}
}
