---
status: accepted
date: 2026-10-27
---

# Version the relay protocol as alpha

## Context and problem statement

dutserver, the relay, is a proof of concept: it keeps nothing across restarts,
has no handshake or keep-alive with its agents, and no TLS. Agents register
their devices with it through `RelayService`, whose comments call it
experimental. Yet the service sat in the protobuf package `dutctl.v1`, next to
`DeviceService`, in the file that the README links as the RPC protocol. That
protocol becomes stable and backwards compatible with v1.0.0
([ADR-0028](0028-tier-the-public-interfaces-for-v1-0-0.md)).

In Go, a major version 0 marks a module that is not stable yet. Protobuf
packages have no version 0: buf's lint rules require a major version of at
least 1. Instead, as in Google's API guidelines, a suffix such as `v1alpha1`
marks a package that is not stable yet, and the package name is part of the
wire path of every call.

How should the protocol show that the promise does not cover the relay?

## Considered options

- **An exception in the README**: keep the service in `dutctl.v1`, and say in
  the README that the promise does not cover it.
- **An alpha package**: move the service to `dutctl.relay.v1alpha1`.

## Decision outcome

Chosen option: **an alpha package**, because the package name then says what
the promise covers, in every generated client and on the wire, and buf's
checks can follow it. As long as the relay is a proof of concept, incompatible
changes to its protocol are likely, and the move costs least now: only dutagent
and dutserver use the service, and they are released together.

- The relay service and its messages live in
  [protobuf/dutctl/relay/v1alpha1/relay.proto](../../protobuf/dutctl/relay/v1alpha1/relay.proto),
  in the package `dutctl.relay.v1alpha1`. They moved without changes to their
  fields.
- This adds to [ADR-0028](0028-tier-the-public-interfaces-for-v1-0-0.md): the RPC
  protocol in its primary tier is the package `dutctl.v1`, which keeps
  `DeviceService` and its messages. The
  [README](../../README.md#public-interfaces) says that the promise does not
  cover the relay protocol or the Go code generated from it.
- buf's breaking-change rules skip packages with an alpha or beta suffix.
- When the relay is ready for the promise, its protocol moves to
  `dutctl.relay.v1`.

### Consequences

- Good, because anyone who calls the relay sees from its name that it may
  still change.
- Good, because buf can check `dutctl.v1` for breaking changes without
  reporting changes to the relay.
- Bad, because the move changed the wire path of the registration: an agent
  and a dutserver from before and after the move cannot register with each
  other, so both are updated together.
- Bad, because a stable relay protocol needs another rename, with the same
  joint update.

### Confirmation

The dutserver tests call `Register` with the messages of the new package.

## More information

- [ADR-0028](0028-tier-the-public-interfaces-for-v1-0-0.md): the interface tiers and the
  road to v1.0.0.
- [ADR-0002](0002-speak-grpc-over-cleartext-http-2-with-connect-go.md): connect-go, the protobuf
  contract and the code generated from it.
- [cmds/exp/dutserver/README.md](../../cmds/exp/dutserver/README.md): what the
  relay does and what it lacks.
- buf's [lint rules](https://buf.build/docs/lint/rules/), among them
  PACKAGE_VERSION_SUFFIX, and Google's [AIP-185](https://google.aip.dev/185)
  on API versioning.
