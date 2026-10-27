---
status: accepted
date: 2026-06-03
---

# Identify callers by the HTTP From header

## Context and problem statement

Device locks ([ADR-0017](0017-lock-devices-in-agent-memory.md)) need an owner. The
agent must tell the holder of a device from other callers, and a refused user
must learn whom to ask. Until then, dutctl sent no caller identity at all.

The need was coordination, not security, so the maintainers wanted a first
identity that is as easy as possible to implement. A verified identity, through
client certificates, needs TLS, and the transport was cleartext HTTP/2 without
it ([ADR-0002](0002-speak-grpc-over-cleartext-http-2-with-connect-go.md)).

How should callers be named for now?

## Decision drivers

- Easy to implement, with no setup for users and CI jobs.
- Clients that send no name keep working.
- The name only has to tell others whom to ask.
- A stronger identity can replace the name later.

## Considered options

- **A self-asserted name in the From header**: the client sends
  `<user>@<host>`, and the agent takes it as given.
- **A header of dutctl's own**: the same name in a custom header.
- **An owner field in the protobuf messages**: on `Command`, `LockRequest` and
  `UnlockRequest`, as first planned.
- **Client certificates (mutual TLS)**: the agent verifies who the caller is,
  from a certificate that each client presents.
- **Shared secrets or API tokens**: each user presents a token that the agent
  checks.

## Decision outcome

Chosen option: **a self-asserted name in the From header**, because it is the
easiest identity that serves the need: no setup, and no change to the RPC
messages. From is a standard header that names the human user behind a request
(RFC 9110, section 10.1.2), so dutctl needs no header of its own.

Client certificates are deferred, not rejected: they give a verified identity,
but need TLS, a certificate authority, and the distribution and rotation of
certificates. The name is the first step, and a verified source can replace it
later ([ADR-0026](0026-defer-authentication-to-mtls.md)). Tokens also need
distribution and rotation, too much just to name a lock holder.

- dutctl sends the caller's name in the `From` header on Run, Lock and Unlock.
  By default it is `<user>@<host>`, from `$USER` and the host name; `-u` sets
  another one.
- The agent takes the name as given; it is not authenticated.
- A caller without the header gets `unknown-` and a random suffix, new on every
  request. A fixed `unknown` would let two such callers claim each other's
  devices.
- The dutserver relay forwards the header to the agent on Run.

### Consequences

- Good, because naming needs no setup, and older clients still run commands,
  under an anonymous name.
- Good, because the request messages carry no caller identity, so a verified
  source can replace the header without changing them.
- Bad, because anyone can claim any name.
- Bad, because all terminals and scripts of one user on one host share a name,
  so the agent cannot tell them apart
  ([ADR-0030](0030-run-one-command-at-a-time-per-device.md)).
- Bad, because a caller without a name gets a new one on every request, so it
  cannot release a lock it took.
- Bad, because a client that cannot read `$USER` or its host name falls back to
  a random name, which differs on every invocation.

### Confirmation

`internal/rpc/identity_test.go` and `internal/auth/auth_test.go` test the
header, the anonymous fallback and the default name.

## More information

- [ADR-0017](0017-lock-devices-in-agent-memory.md): the locks that need an owner.
- [ADR-0026](0026-defer-authentication-to-mtls.md): the way to a verified
  identity, and the one place where the agent resolves the identity.
- [pkg/headers](../../pkg/headers) names the header, and
  [internal/auth](../../internal/auth) models the identity.
- [RFC 9110, section 10.1.2](https://www.rfc-editor.org/rfc/rfc9110.html#section-10.1.2)
  defines the From header.
- Later changes:
  - 2026-07: the header name moved from `pkg/lock` to the new `pkg/headers`,
    which also names the version header and declares both part of the wire
    contract for other client implementations.
  - 2026-07: Lock and a normal Unlock began to refuse a caller without a name.

Recorded retroactively in October 2026.
