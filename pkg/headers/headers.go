// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package headers defines the HTTP headers that carry dutctl RPC metadata
// alongside the protobuf messages. They are part of the wire contract, so any
// client implementation sets and reads them: User identifies the caller and
// Version drives the build-compatibility handshake.
package headers

// User is the HTTP header that carries the requesting user's identity from the
// client to the agent. dutctl reuses the standard "From" header (RFC 9110
// section 10.1.2), whose defined purpose is to identify the human user
// controlling the requesting agent. Identity travels in a header rather than in
// proto messages so that older clients, which omit it, remain compatible.
const User = "From"

// Version is the HTTP header on which the client and agent advertise their
// dutctl build version for the compatibility handshake: the client stamps it on
// requests, the agent on responses.
const Version = "Dutctl-Version"
