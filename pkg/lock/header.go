// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package lock defines constants shared between the dutctl client and the
// dutagent for the per-device locking feature.
package lock

// UserHeader is the HTTP header used to carry the requesting user's
// identity from the client to the agent. We reuse the standard "From"
// header (RFC 9110 section 10.1.2): its defined purpose is to identify the
// human user controlling the requesting user agent. User identity travels
// in the header rather than in proto messages so that older clients (which
// omit it) remain compatible.
const UserHeader = "From"
