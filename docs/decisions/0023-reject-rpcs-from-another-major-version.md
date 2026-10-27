---
status: accepted
date: 2026-07-10
---

# Reject RPCs from another major version

## Context and problem statement

dutctl, dutagent and the experimental dutserver are built from one repository
and share one release version. They are installed separately, though: the
client on a user's machine, the agent next to the devices. A client and an
agent that do not fit together should notice at once, instead of failing later
with a confusing protocol error. Since May 2026 the release version was
compiled into dutctl and dutagent as a constant that release-please bumps on
each release, but no RPC carried it.

## Considered options

- **A dedicated Version RPC**: the client asks the agent for its version in a
  call of its own.
- **A header on every RPC, checked by the client**: the agent sends its version
  with every response; the client compares and fails on a major mismatch.
- **A header on every RPC, enforced by the agent**: both sides send their
  version; the agent refuses an incompatible client, the client only warns.

## Decision outcome

Chosen option: **a header on every RPC, enforced by the agent**, because the
agent guards its own contract and is the only side that can refuse to run a
command; a client can only decline to go on. Connect interceptors put the
header on every call, the streaming Run included, without a call of its own.

- Client and agent send their release version in the `Dutctl-Version` HTTP
  header: the client on every request, the agent on its responses.
- The agent compares the client's version with its own before the handler
  runs. A different major version is incompatible: the agent rejects the call
  with `FailedPrecondition`, so no module runs.
- Any other difference passes. The client warns once about a different minor
  version, a different pre-release tag, or a version that is missing or does
  not parse. A different patch version is silent.
- The client never fails a call over the version.
- The version is the constant `buildinfo.Version` in
  [internal/buildinfo](../../internal/buildinfo), which release-please bumps on
  every release.
- The policy lives in [internal/compat](../../internal/compat). It holds the
  thresholds but no wording; the messages stay with the callers.
- The experimental dutserver relay passes both headers through on Run and
  refuses a peer with another major version itself, as it cannot bridge
  messages between major versions.

### Consequences

- Good, because a client and an agent of different major versions fail on the
  first call, with a message that names both versions.
- Good, because one policy function serves the agent, the client and the relay.
- Bad, because all `1.0.0-alpha.N` releases share major version 1, so during
  the alpha train a breaking protocol change between two alphas only warns.
- Neutral, because the agent accepts a client that sends no version, so a
  client written outside this repository is checked only if it sets the header.

### Confirmation

`TestCompare` in [internal/compat](../../internal/compat) pins the policy.
`TestVersionHandshake` in [internal/rpc](../../internal/rpc) runs both
interceptors over HTTP: a patch difference passes silently, a minor difference
warns, and a major difference is rejected with `FailedPrecondition`.

## More information

- [ADR-0002](0002-speak-grpc-over-cleartext-http-2-with-connect-go.md): the connect transport the
  interceptors plug into.
- [ADR-0013](0013-release-v1-0-0-as-alpha-pre-releases.md): the alpha
  pre-release train.
- [ADR-0028](0028-tier-the-public-interfaces-for-v1-0-0.md): which interfaces are promised
  stable from v1.0.0.
- [internal/rpc/version.go](../../internal/rpc/version.go): the two
  interceptors; [pkg/headers](../../pkg/headers): the header names that belong
  to the wire contract.
- Later changes:
  - 2026-07: the header was renamed from `X-Dutctl-Version` to
    `Dutctl-Version`, a breaking change, and published with the caller header
    in the public `pkg/headers` package. The policy moved into
    `internal/compat` and the interceptors into `internal/rpc`. The dutserver
    relay answered another major version with a `FailedPrecondition` error
    instead of a panic.

Recorded retroactively in October 2026.
