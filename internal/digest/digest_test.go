// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package digest_test

import (
	"testing"

	"github.com/BlindspotSoftware/dutctl/internal/digest"
)

// knownSum is the SHA-256 of "abc", the canonical NIST test vector, so a change
// of algorithm cannot pass unnoticed.
const knownSum = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

func TestSumKnownVector(t *testing.T) {
	if got := digest.Hex(digest.Sum([]byte("abc"))); got != knownSum {
		t.Errorf("Sum(\"abc\") = %s, want %s", got, knownSum)
	}
}

func TestSumLength(t *testing.T) {
	if got := len(digest.Sum(nil)); got != 32 {
		t.Errorf("digest length = %d, want 32", got)
	}
}

func TestMatch(t *testing.T) {
	content := []byte("firmware")
	sum := digest.Sum(content)

	if !digest.Match(sum, digest.Sum(content)) {
		t.Error("Match on identical content = false, want true")
	}

	// A single flipped byte, the shape the transport corruption took.
	corrupt := append([]byte(nil), content...)
	corrupt[0] ^= 0x01

	if digest.Match(digest.Sum(corrupt), sum) {
		t.Error("Match on altered content = true, want false")
	}
}

func TestMissing(t *testing.T) {
	tests := []struct {
		name string
		want []byte
		miss bool
	}{
		{"nil", nil, true},
		{"empty", []byte{}, true},
		{"present", digest.Sum([]byte("x")), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := digest.Missing(tt.want); got != tt.miss {
				t.Errorf("Missing = %v, want %v", got, tt.miss)
			}
		})
	}
}
