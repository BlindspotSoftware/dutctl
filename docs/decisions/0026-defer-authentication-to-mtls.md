---
status: accepted
date: 2026-07-24
---

# Defer authentication to mTLS

## Context and problem statement

From its first version, DUT Control runs its RPCs over cleartext HTTP/2 (h2c),
without TLS and without authentication. That holds for every hop: dutctl to
dutagent, dutctl through the dutserver relay to dutagent, and the agent's
registration with the relay. Anyone who reaches an agent can run its commands.

Module credentials, such as IPMI, PDU or SSH passwords, sit in plain text in the
agent configuration, and trust rests on the lab network.

Device locks brought a first, easy caller identity: a name the client asserts
in the From header ([ADR-0018](0018-identify-callers-by-the-http-from-header.md)).
A verified identity through client certificates was left for later. The Lock,
Unlock and Run handlers each read the header themselves, so that later step
would touch every handler.

Should callers be authenticated now, and if not, how is the way to a verified
identity kept open?

## Decision drivers

- Locks coordinate users who cooperate and are no security boundary
  ([ADR-0017](0017-lock-devices-in-agent-memory.md)); a name that callers assert
  serves them.
- No setup for agents and clients.
- A verified identity must be able to replace the asserted name later without
  changing the handlers.

## Considered options

- **Resolve the asserted identity once, in an interceptor**: handlers read it
  from the context, and mutual TLS (mTLS) replaces the header there later.
- **Read the header in each handler**: as Lock, Unlock and Run did.
- **Authenticate with mutual TLS now**: the agent checks a certificate that
  each client presents.

## Decision outcome

Chosen option: **resolve the asserted identity once, in an interceptor**,
because it needs no setup now and leaves one place to change for mutual TLS.
Reading the header in each handler ties every handler to the transport. Mutual
TLS now would bring the certificate setup that
[ADR-0018](0018-identify-callers-by-the-http-from-header.md) left for later.

- Callers stay unauthenticated for now; agents rely on the network they run in.
- A connect interceptor, `rpc.NewIdentifier`, resolves the caller's identity
  once per request into an `auth.Identity` on the request context. Handlers
  read it with `auth.FromContext` and never look at headers.
- Mutual TLS is the intended way to authenticate callers later. The
  interceptor then takes the identity from the client certificate instead of
  the From header, and the handlers stay as they are.
- `Identity` is a struct, so a verified principal, such as a certificate
  subject or roles, can be added without changing the code that reads it.
- Restricting `unlock force` and keeping locks across agent restarts wait until
  callers are authenticated.

### Consequences

- Good, because nothing needs to be set up, and any client that sets the From
  header works.
- Good, because a new identity source changes the interceptor, not the
  handlers.
- Bad, because anyone who reaches an agent can run its commands and force the
  release of any reservation.
- Bad, because commands, their output and transferred files cross the network
  unencrypted.

### Confirmation

`internal/rpc/identity_test.go` tests the interceptor. The handler tests in
`cmds/dutagent/rpc_test.go` put the identity on the context, as the interceptor
does.

## More information

- [ADR-0002](0002-speak-grpc-over-cleartext-http-2-with-connect-go.md): the cleartext HTTP/2
  transport.
- [ADR-0014](0014-let-the-agent-access-client-files-the-user-did-not-name.md): the agent
  names file paths on the client, so the client trusts the agent it reaches.
- [ADR-0017](0017-lock-devices-in-agent-memory.md): the locks, which coordinate users
  who cooperate.
- [ADR-0018](0018-identify-callers-by-the-http-from-header.md): the asserted name,
  and why client certificates were left for later.
- [internal/auth](../../internal/auth) models the identity, and
  [internal/rpc/identity.go](../../internal/rpc/identity.go) resolves it.
- [cmds/exp/dutserver/README.md](../../cmds/exp/dutserver/README.md) and
  [pkg/module/ipmi/README.md](../../pkg/module/ipmi/README.md) warn about the
  missing TLS and the plain-text passwords.

Recorded retroactively in October 2026.
