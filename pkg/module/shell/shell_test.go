// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package shell

import (
	"context"
	"strings"
	"testing"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
)

func newShell(t *testing.T, quiet bool) *Shell {
	t.Helper()

	s := &Shell{Quiet: quiet}
	if err := s.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	return s
}

func TestInitDefaultsAndChecksThePath(t *testing.T) {
	if s := newShell(t, false); s.Path != DefaultShellPath {
		t.Errorf("an unset path is %q, want %q", s.Path, DefaultShellPath)
	}

	if err := (&Shell{Path: "/no/such/shell"}).Init(context.Background()); err == nil {
		t.Error("Init accepted a shell that does not exist")
	}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name    string
		quiet   bool
		command string
		wantOut string
		wantErr string
	}{
		{"stdout and stderr", false, "echo out; echo err >&2", "out\nerr\n", ""},
		{"a failure", false, "echo out; exit 3", "out\n", "exited with code 3"},
		{"quiet forwards stderr", true, "echo out; echo err >&2", "err\n", ""},
		{"quiet forwards stderr of a failure", true, "echo out; echo err >&2; exit 1", "err\n", "exited with code 1"},
		{"quiet and silent", true, "echo out", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &mock.Session{}

			err := newShell(t, tt.quiet).Run(context.Background(), s, tt.command)
			if tt.wantErr == "" && err != nil {
				t.Errorf("Run: %v", err)
			}

			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("Run returned %v, want an error with %q", err, tt.wantErr)
			}

			if s.PrintText != tt.wantOut {
				t.Errorf("forwarded %q, want %q", s.PrintText, tt.wantOut)
			}
		})
	}
}

func TestRunWantsOneCommandString(t *testing.T) {
	for _, args := range [][]string{nil, {"echo", "two"}} {
		if err := newShell(t, false).Run(context.Background(), &mock.Session{}, args...); err == nil {
			t.Errorf("Run accepted %q", args)
		}
	}
}
