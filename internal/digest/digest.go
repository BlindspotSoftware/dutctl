// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package digest computes and compares the content digests that accompany file
// transfers between the dutctl client and the dutagent.
//
// Connect and TCP validate framing, not content. A path that damages bytes and
// recomputes checksums (a NIC offload engine re-splitting packets for a
// userspace VPN, for one) delivers a full-length, well-formed, wrong file that
// nothing else in dutctl would notice.
package digest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// ErrMismatch reports content that does not hash to the digest sent with it.
// Callers wrap it with the file and both digests.
var ErrMismatch = errors.New("content does not match its digest")

// Sum returns the SHA-256 of content, the form carried in File.sha256.
func Sum(content []byte) []byte {
	sum := sha256.Sum256(content)

	return sum[:]
}

// Missing reports whether a peer omitted the digest, which happens only when it
// predates the field. Kept apart from [Match] so an absent digest can warn
// while a wrong one fails.
func Missing(want []byte) bool {
	return len(want) == 0
}

// Match reports whether a digest computed by [Sum] equals the one received.
func Match(sum, want []byte) bool {
	return bytes.Equal(sum, want)
}

// Hex renders a digest as lowercase hex. Abbreviating it for display is left to
// the caller.
func Hex(sum []byte) string {
	return hex.EncodeToString(sum)
}
