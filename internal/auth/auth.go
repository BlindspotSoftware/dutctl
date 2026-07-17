// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package auth models the identity of the caller behind a dutagent RPC and
// carries it on the request context, so the RPC handlers never depend on how
// that identity was established.
//
// An identity is caller-asserted today (taken from a request header) and is
// therefore unauthenticated; [Identity.IsAnonymous] marks a caller that
// asserted none. Identity is a struct so a verified principal — a certificate
// subject, roles — can be added when transport authentication (mTLS) lands,
// without changing callers that read it back with [FromContext].
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
)

// anonymousSuffixBytes is the random-byte length appended to an anonymous
// identity; rendered as hex, the visible suffix is twice this.
const anonymousSuffixBytes = 4

// Identity is the identity of the caller behind a request. The zero value is
// not a usable identity; construct one with [Named], [Anonymous] or [Default].
type Identity struct {
	user      string
	anonymous bool
}

// Named returns the identity of a caller that asserted the identity user (e.g.
// in a request header). It is not authenticated; see the package documentation.
func Named(user string) Identity {
	return Identity{user: user}
}

// Anonymous returns a fresh, unique identity for a caller that asserted none.
// The random suffix keeps unrelated anonymous callers from collapsing onto one
// identity and thereby operating on each other's locked devices.
func Anonymous() Identity {
	return Identity{user: "unknown-" + randSuffix(anonymousSuffixBytes), anonymous: true}
}

// Default returns the identity an interactive client uses when the user does
// not pass one explicitly: "<user>@<host>". The value is deterministic so
// repeated invocations from the same shell share an identity and can release a
// lock they took. It falls back to [Anonymous] when the user or hostname cannot
// be read.
func Default() Identity {
	user := os.Getenv("USER")

	host, err := os.Hostname()
	if user == "" || err != nil || host == "" {
		return Anonymous()
	}

	return Named(fmt.Sprintf("%s@%s", user, host))
}

// User returns the identity as the string used to key a device lock.
func (i Identity) User() string {
	return i.user
}

// IsAnonymous reports whether the caller asserted no identity and was assigned
// an anonymous placeholder.
func (i Identity) IsAnonymous() bool {
	return i.anonymous
}

// ctxKey is the unexported key under which the caller identity is stored on a
// context, so no other package can collide with or overwrite it.
type ctxKey struct{}

// NewContext returns a copy of ctx carrying the caller's identity, read back
// with [FromContext].
func NewContext(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the caller's identity carried by ctx. ok is false when no
// identity was attached, i.e. the identity interceptor did not run.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)

	return id, ok
}

func randSuffix(n int) string {
	buf := make([]byte, n)
	// crypto/rand.Read never returns a non-nil error on Go 1.24+ (it reads the OS
	// CSPRNG and panics internally if that ever fails), so discarding the error is
	// safe and keeps randSuffix infallible.
	_, _ = rand.Read(buf)

	return hex.EncodeToString(buf)
}
