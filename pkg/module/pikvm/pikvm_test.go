// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pikvm

import (
	"strings"
	"testing"
)

func TestInitWithDefaults(t *testing.T) {
	p := &PiKVM{
		Host:     "pikvm.local",
		Password: "admin",
		Command:  "power",
	}

	err := p.Init()
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Check default user is set
	if p.User != defaultUser {
		t.Fatalf("expected default user %q, got %q", defaultUser, p.User)
	}

	// Check baseURL is properly set
	if p.baseURL == nil {
		t.Fatalf("baseURL is nil")
	}

	// Check HTTPS is added if no scheme provided
	if p.baseURL.Scheme != "https" {
		t.Fatalf("expected https scheme, got %q", p.baseURL.Scheme)
	}
}

func TestInitWithExplicitHTTP(t *testing.T) {
	p := &PiKVM{
		Host:     "http://192.168.1.100",
		User:     "admin",
		Password: "admin",
		Command:  "keyboard",
	}

	err := p.Init()
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if p.baseURL.Scheme != "http" {
		t.Fatalf("expected http scheme, got %q", p.baseURL.Scheme)
	}
}

func TestInitWithCustomTimeout(t *testing.T) {
	p := &PiKVM{
		Host:     "pikvm.local",
		User:     "admin",
		Password: "admin",
		Timeout:  "30s",
		Command:  "media",
	}

	err := p.Init()
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Just verify it doesn't fail - actual timeout value is internal to client
}

func TestInitWithInvalidTimeout(t *testing.T) {
	p := &PiKVM{
		Host:     "pikvm.local",
		User:     "admin",
		Password: "admin",
		Timeout:  "invalid",
		Command:  "screenshot",
	}

	// Should still succeed but fall back to default timeout
	err := p.Init()
	if err != nil {
		t.Fatalf("Init should succeed with invalid timeout (falls back to default): %v", err)
	}
}

func TestInitWithoutHost(t *testing.T) {
	p := &PiKVM{
		User:     "admin",
		Password: "admin",
	}

	err := p.Init()
	if err == nil {
		t.Fatalf("Init should fail when Host is not set")
	}

	if !strings.Contains(err.Error(), "host is not set") {
		t.Fatalf("expected error about host not set, got: %v", err)
	}
}

func TestHelp(t *testing.T) {
	p := &PiKVM{
		Host: "pikvm.local",
	}

	help := p.Help()
	if help == "" {
		t.Fatalf("Help returned empty string")
	}

	// Check that help contains key sections
	expectedSections := []string{
		"PiKVM Control Module",
		"Power Management",
		"Keyboard Control",
		"Virtual Media",
		"on",
		"off",
		"force-off",
		"reset",
		"type",
		"key",
		"key-combo",
		"mount",
		"unmount",
	}

	for _, section := range expectedSections {
		if !strings.Contains(help, section) {
			t.Fatalf("Help should contain %q, but doesn't", section)
		}
	}
}

func TestDeinit(t *testing.T) {
	p := &PiKVM{}

	err := p.Deinit()
	if err != nil {
		t.Fatalf("Deinit should not return error: %v", err)
	}
}

func TestParseKeyCombo(t *testing.T) {
	// Test key combination parsing
	comboStr := "Ctrl+Alt+Delete"
	keys := strings.Split(comboStr, "+")

	expectedKeys := []string{"Ctrl", "Alt", "Delete"}
	if len(keys) != len(expectedKeys) {
		t.Fatalf("expected %d keys, got %d", len(expectedKeys), len(keys))
	}

	for i, key := range keys {
		trimmed := strings.TrimSpace(key)
		if trimmed != expectedKeys[i] {
			t.Fatalf("expected key %q at position %d, got %q", expectedKeys[i], i, trimmed)
		}
	}
}
