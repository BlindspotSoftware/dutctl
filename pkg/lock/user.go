// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lock

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
)

// anonymousSuffixBytes is the random-byte length appended to anonymous
// identities; rendered as hex, the visible suffix length is twice this.
const anonymousSuffixBytes = 4

// DefaultUser returns the identity used by interactive clients when the user
// did not pass one explicitly: "<user>@<host>". The value is deterministic so
// subsequent invocations from the same shell can release a lock they took.
// When USER or hostname cannot be read, the caller is effectively anonymous
// and AnonymousUser is returned to keep concurrent anonymous callers from
// colliding on a single identity.
func DefaultUser() string {
	user := os.Getenv("USER")
	host, hostErr := os.Hostname()

	if user == "" || hostErr != nil || host == "" {
		return AnonymousUser()
	}

	return fmt.Sprintf("%s@%s", user, host)
}

// AnonymousUser returns the placeholder identity assigned by the agent to a
// caller whose identity could not be determined, e.g. when the request omits
// UserHeader. The random suffix prevents unrelated anonymous callers from
// colliding on a single shared identity.
func AnonymousUser() string {
	return "unknown-" + randSuffix(anonymousSuffixBytes)
}

func randSuffix(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)

	return hex.EncodeToString(buf)
}
