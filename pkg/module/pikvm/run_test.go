// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pikvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
)

// fakePiKVM records the requests it gets. GET /api/msd answers with status,
// every other request with {"ok": true}, unless fail says to answer with 500.
type fakePiKVM struct {
	status string
	fail   func(r *http.Request) bool
	seen   func(r *http.Request) // called with every request before it is answered

	mu    sync.Mutex
	calls []string // method, path and query of each request
}

func (f *fakePiKVM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)

	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	f.mu.Unlock()

	if f.seen != nil {
		f.seen(r)
	}

	switch {
	case f.fail != nil && f.fail(r):
		http.Error(w, "boom", http.StatusInternalServerError)
	case r.URL.Path == "/api/msd":
		_, _ = io.WriteString(w, f.status)
	default:
		_, _ = io.WriteString(w, `{"ok": true}`)
	}
}

// called reports whether a request contained want.
func (f *fakePiKVM) called(want string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.ContainsFunc(f.calls, func(c string) bool { return strings.Contains(c, want) })
}

// startPiKVM returns a module in mode, initialized against f.
func startPiKVM(t *testing.T, f *fakePiKVM, mode string) *PiKVM {
	t.Helper()

	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	p := &PiKVM{Host: srv.URL, Password: "secret", Mode: mode}

	err := p.Init(t.Context())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	return p
}

// A missing argument fails the run, so a script sees the failure.
func TestRunMissingArgumentsFail(t *testing.T) {
	for _, tc := range []struct {
		mode string
		args []string
	}{
		{cmdTypePower, nil},
		{cmdTypeKeyboard, nil},
		{cmdTypeKeyboard, []string{"type"}},
		{cmdTypeKeyboard, []string{"--delay"}},
		{cmdTypeMedia, nil},
		{cmdTypeMedia, []string{"mount"}},
		{cmdTypeMedia, []string{"mount-url"}},
	} {
		p := startPiKVM(t, &fakePiKVM{}, tc.mode)

		err := p.Run(t.Context(), &mock.Session{}, tc.args...)
		if err == nil {
			t.Errorf("%s %q: Run returned nil, want an error", tc.mode, tc.args)
		}
	}
}

// A power action and a click return once the PiKVM pressed the button, so a
// status read right after sees the effect.
func TestPowerWaitsForThePress(t *testing.T) {
	f := &fakePiKVM{}
	p := startPiKVM(t, f, cmdTypePower)

	for _, action := range []string{"on", "reset"} {
		err := p.Run(t.Context(), &mock.Session{}, action)
		if err != nil {
			t.Fatalf("Run %s: %v", action, err)
		}
	}

	for _, want := range []string{"/api/atx/power?action=on&wait=1", "/api/atx/click?button=reset&wait=1"} {
		if !f.called(want) {
			t.Errorf("no request %s; requests: %q", want, f.calls)
		}
	}
}

// A key combination that fails halfway releases the modifiers it pressed, or
// they stay held on the DUT.
func TestKeyComboReleasesHeldKeysOnFailure(t *testing.T) {
	f := &fakePiKVM{fail: func(r *http.Request) bool {
		return r.URL.Query().Get("key") == "Delete"
	}}
	p := startPiKVM(t, f, cmdTypeKeyboard)

	err := p.Run(t.Context(), &mock.Session{}, "--delay", "1ms", "key-combo", "Ctrl+Alt+Delete")
	if err == nil {
		t.Fatal("Run returned nil, want the failure of the main key")
	}

	for _, key := range []string{"key=AltLeft&state=0", "key=ControlLeft&state=0"} {
		if !f.called(key) {
			t.Errorf("no release %s; requests: %q", key, f.calls)
		}
	}
}

// A cancelled run ends a key combination at once, and still releases the
// modifiers it pressed.
func TestKeyComboReleasesHeldKeysOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	f := &fakePiKVM{seen: func(r *http.Request) {
		if r.URL.Query().Get("state") == "1" {
			cancel()
		}
	}}
	p := startPiKVM(t, f, cmdTypeKeyboard)

	err := p.Run(ctx, &mock.Session{}, "--delay", "1h", "key-combo", "Ctrl+Delete")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled", err)
	}

	if !f.called("key=ControlLeft&state=0") {
		t.Errorf("Ctrl not released; requests: %q", f.calls)
	}
}

// statusJSON is the MSD status of an idle PiKVM with free storage and the
// given images.
func statusJSON(images string) string {
	return `{"ok": true, "result": {"enabled": true, "online": true,
		"drive": {"connected": false},
		"storage": {"images": {` + images + `}, "parts": {"": {"free": 1073741824, "writable": true}}}}}`
}

// A mounted file comes from the client and is stored on the PiKVM under its
// SHA-256.
func TestMountUploadsTheClientsFile(t *testing.T) {
	content := []byte("bootable image")
	sum := sha256.Sum256(content)
	hashSum := hex.EncodeToString(sum[:])

	f := &fakePiKVM{status: statusJSON("")}
	p := startPiKVM(t, f, cmdTypeMedia)
	s := &mock.Session{RequestedFileResponse: bytes.NewReader(content)}

	err := p.Run(t.Context(), s, "mount", "images/boot.iso")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if s.RequestedFileName != "images/boot.iso" {
		t.Errorf("requested %q, want images/boot.iso", s.RequestedFileName)
	}

	for _, want := range []string{"/api/msd/write?image=" + hashSum, "/api/msd/set_params?cdrom=0&image=" + hashSum} {
		if !f.called(want) {
			t.Errorf("no request %s; requests: %q", want, f.calls)
		}
	}
}

// An image from a URL is named after the last element of the URL's path, not
// after its query.
func TestMountURLNamesTheImageWithoutTheQuery(t *testing.T) {
	f := &fakePiKVM{status: statusJSON(`"fw.iso": {"complete": true}`)}
	p := startPiKVM(t, f, cmdTypeMedia)

	err := p.Run(t.Context(), &mock.Session{}, "mount-url", "https://example.com/dl/fw.iso?token=a&sig=b")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !f.called("/api/msd/set_params?cdrom=0&image=fw.iso&rw=1") {
		t.Errorf("image not configured as fw.iso; requests: %q", f.calls)
	}
}
