// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ipmi

import (
	"strings"
	"testing"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
)

// TestRunRejectsCommand checks that a missing or unknown command fails the run
// with the available commands, before the module talks to the BMC.
func TestRunRejectsCommand(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}} {
		i := &IPMI{Host: "bmc.invalid"}

		err := i.Run(t.Context(), &mock.Session{}, args...)
		if err == nil || !strings.Contains(err.Error(), commands) {
			t.Fatalf("Run(%q) = %v, want an error naming %q", args, err, commands)
		}
	}
}
