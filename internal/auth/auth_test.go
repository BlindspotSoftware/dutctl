// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package auth

import (
	"context"
	"strings"
	"testing"
)

func TestNamed(t *testing.T) {
	id := Named("alice@host")

	if id.User() != "alice@host" {
		t.Errorf("User = %q, want alice@host", id.User())
	}

	if id.IsAnonymous() {
		t.Error("Named identity reports anonymous")
	}
}

func TestAnonymousUniqueAndFlagged(t *testing.T) {
	first := Anonymous()
	second := Anonymous()

	if !first.IsAnonymous() {
		t.Error("Anonymous identity does not report anonymous")
	}

	if !strings.HasPrefix(first.User(), "unknown-") {
		t.Errorf("anonymous user = %q, want unknown-<rand> prefix", first.User())
	}

	// Two anonymous callers must not collapse onto one identity, or one could
	// operate on a device the other locked.
	if first.User() == second.User() {
		t.Errorf("two anonymous identities shared user %q", first.User())
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := NewContext(context.Background(), Named("alice@host"))

	id, ok := FromContext(ctx)
	if !ok {
		t.Fatal("FromContext ok = false, want true")
	}

	if id.User() != "alice@host" {
		t.Errorf("User = %q, want alice@host", id.User())
	}
}

func TestFromContextUnset(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Error("FromContext ok = true on a bare context, want false")
	}
}

func TestDefaultNamed(t *testing.T) {
	t.Setenv("USER", "carol")

	id := Default()

	// os.Hostname resolves in the test environment, so Default yields a named
	// user@host identity.
	if id.IsAnonymous() {
		t.Fatal("Default reported anonymous with USER set")
	}

	if !strings.HasPrefix(id.User(), "carol@") {
		t.Errorf("Default user = %q, want carol@<host>", id.User())
	}
}

func TestDefaultFallsBackToAnonymous(t *testing.T) {
	t.Setenv("USER", "")

	if id := Default(); !id.IsAnonymous() {
		t.Errorf("Default with empty USER = %q, want anonymous", id.User())
	}
}
