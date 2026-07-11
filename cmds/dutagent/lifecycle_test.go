// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// lifecycleModule is a module test double that records whether Init/Deinit were
// called and can be configured to fail (via an error or a panic).
type lifecycleModule struct {
	initErr      error
	deinitErr    error
	panicOnInit  bool
	initCalled   bool
	deinitCalled bool
}

func (m *lifecycleModule) Help() string { return "lifecycle test module" }

func (m *lifecycleModule) Init(_ context.Context) error {
	m.initCalled = true

	if m.panicOnInit {
		panic("boom")
	}

	return m.initErr
}

func (m *lifecycleModule) Deinit(_ context.Context) error {
	m.deinitCalled = true

	return m.deinitErr
}

func (m *lifecycleModule) Run(_ context.Context, _ module.Session, _ ...string) error { return nil }

func wrap(name string, m module.Module) dut.Module {
	return dut.Module{Module: m, Config: dut.ModuleConfig{Name: name}}
}

// TestInitModulesAggregatesAndRunsAll verifies the two contracts of initModules:
// every module is initialized even after one fails, and all reported failures
// (both returned errors and recovered panics) aggregate into one *moduleInitError.
func TestInitModulesAggregatesAndRunsAll(t *testing.T) {
	good1 := &lifecycleModule{}
	bad := &lifecycleModule{initErr: errors.New("init failed")}
	good2 := &lifecycleModule{}
	boom := &lifecycleModule{panicOnInit: true}

	devs := dut.Devlist{
		"devA": {Cmds: map[string]dut.Command{
			"cmd1": {Modules: []dut.Module{wrap("good1", good1), wrap("bad", bad)}},
		}},
		"devB": {Cmds: map[string]dut.Command{
			"cmd2": {Modules: []dut.Module{wrap("good2", good2), wrap("boom", boom)}},
		}},
	}

	err := initModules(context.Background(), devs)

	// Run-all-on-error: every module's Init must have been invoked.
	for _, tc := range []struct {
		name string
		mod  *lifecycleModule
	}{{"good1", good1}, {"bad", bad}, {"good2", good2}, {"boom", boom}} {
		if !tc.mod.initCalled {
			t.Errorf("module %q was not initialized (run-all-on-error violated)", tc.name)
		}
	}

	// The failing module and the panicking module aggregate into one error with two details.
	var initErr *moduleInitError
	if !errors.As(err, &initErr) {
		t.Fatalf("expected *moduleInitError, got %T (%v)", err, err)
	}

	if len(initErr.Errs) != 2 {
		t.Fatalf("expected 2 aggregated errors, got %d", len(initErr.Errs))
	}
}

func TestInitModulesSuccess(t *testing.T) {
	m := &lifecycleModule{}
	devs := dut.Devlist{
		"devA": {Cmds: map[string]dut.Command{
			"cmd1": {Modules: []dut.Module{wrap("m", m)}},
		}},
	}

	if err := initModules(context.Background(), devs); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !m.initCalled {
		t.Error("module was not initialized")
	}
}

// TestDeinitModulesAggregatesAndRunsAll mirrors the init test for the shutdown path.
func TestDeinitModulesAggregatesAndRunsAll(t *testing.T) {
	good := &lifecycleModule{}
	bad := &lifecycleModule{deinitErr: errors.New("deinit failed")}

	devs := dut.Devlist{
		"devA": {Cmds: map[string]dut.Command{
			"cmd1": {Modules: []dut.Module{wrap("good", good), wrap("bad", bad)}},
		}},
	}

	err := deinitModules(context.Background(), devs)

	if !good.deinitCalled || !bad.deinitCalled {
		t.Error("not all modules were deinitialized (run-all-on-error violated)")
	}

	var initErr *moduleInitError
	if !errors.As(err, &initErr) {
		t.Fatalf("expected *moduleInitError, got %T (%v)", err, err)
	}

	if len(initErr.Errs) != 1 {
		t.Fatalf("expected 1 aggregated error, got %d", len(initErr.Errs))
	}
}
