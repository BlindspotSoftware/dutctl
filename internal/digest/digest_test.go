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

// digestOf digests content in one write.
func digestOf(content []byte) []byte {
	h := digest.New()
	h.Write(content)

	return h.Sum(nil)
}

// A file split into chunks digests the same as the whole file.
func TestNewKnownVector(t *testing.T) {
	h := digest.New()
	h.Write([]byte("a"))
	h.Write([]byte("bc"))

	if got := digest.Hex(h.Sum(nil)); got != knownSum {
		t.Errorf("digest of \"a\" then \"bc\" = %s, want %s", got, knownSum)
	}
}

func TestNewLength(t *testing.T) {
	if got := len(digestOf(nil)); got != 32 {
		t.Errorf("digest length = %d, want 32", got)
	}
}

func TestMatch(t *testing.T) {
	content := []byte("firmware")
	sum := digestOf(content)

	if !digest.Match(sum, digestOf(content)) {
		t.Error("Match on identical content = false, want true")
	}

	// A single flipped byte, the shape the transport corruption took.
	corrupt := append([]byte(nil), content...)
	corrupt[0] ^= 0x01

	if digest.Match(digestOf(corrupt), sum) {
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
		{"present", digestOf([]byte("x")), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := digest.Missing(tt.want); got != tt.miss {
				t.Errorf("Missing = %v, want %v", got, tt.miss)
			}
		})
	}
}
