// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package main

import (
	"errors"
	"io/fs"
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

// TestRepositoryConfigs loads every agent configuration of the repository as
// dutagent -check-config does: the module examples, among them the demo
// modules' one that the README uses, the configuration of the hardware tests
// and the dutserver's examples. A change of the grammar or of a module's
// options then fails here, in the commit that makes it, rather than for the
// user who tries the example. The testdata directories hold invalid
// configurations on purpose.
func TestRepositoryConfigs(t *testing.T) {
	var paths []string

	root := filepath.Join("..", "..")

	for _, dir := range []string{"cmds/exp/contrib", "pkg/module", "test"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case entry.IsDir() && entry.Name() == "testdata":
				return fs.SkipDir
			case filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml":
				paths = append(paths, path)
			}

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// A wrong root would find nothing and pass.
	if len(paths) < 10 {
		t.Fatalf("found only %d configurations: %v", len(paths), paths)
	}

	for _, path := range paths {
		name, _ := filepath.Rel(root, path)

		t.Run(name, func(t *testing.T) {
			agt := &agent{configPath: path}

			if err := agt.loadConfig(); err != nil {
				t.Error(err)
			}
		})
	}
}
