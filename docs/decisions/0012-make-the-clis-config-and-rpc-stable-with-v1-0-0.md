---
status: superseded by ADR-0028
date: 2025-07-27
---

# Make the CLIs, config and RPC stable with v1.0.0

## Context and problem statement

Since 2024, the plan for v1 had been to harden and document the command-line
interfaces and the scheme of the configuration files. In July 2025, the
maintainers prepared a public pre-release, so that more users could try the
interfaces before v1 fixed them.

A review of the API and the features with regard to backwards compatibility
had been open since September 2024. In July 2025, two of its questions moved
to the Design Proposals discussions: the names of the configuration keys
([ADR-0015](0015-name-the-configuration-keys-after-github-actions.md)), and whether the
client checked transferred files too strictly
([ADR-0014](0014-let-the-agent-access-client-files-the-user-did-not-name.md)).

The README was rewritten for the pre-release; the change gave no reasons for
the interfaces it named. Which interfaces should v1 keep stable and backwards
compatible?

## Decision outcome

The README names three public interfaces, which become stable with the first
major release, v1.0.0:

- the command-line interfaces of the client `dutctl`, the agent `dutagent`
  and the relay server, a proof of concept at the time;
- the agent configuration, specified in
  [docs/dutagent-config.md](../dutagent-config.md);
- the RPC protocol for calling agents, defined in
  [protobuf/dutctl/v1/dutctl.proto](../../protobuf/dutctl/v1/dutctl.proto).

Until then, they are not yet stable.

### Consequences

- Good, because users of the pre-releases know which interfaces they can rely
  on from v1.0.0.
- Bad, because from v1.0.0 on, an incompatible change to a command or a flag
  of the three programs needs a new major version, as one to the configuration
  or the protocol does.
- Neutral, because the list names no Go package, not even the module API in
  `pkg/module`, although the same README invites users to write their own
  modules.

## More information

- Superseded by [ADR-0028](0028-tier-the-public-interfaces-for-v1-0-0.md), which keeps
  the agent configuration and the RPC protocol in the compatibility promise
  and moves the command-line interfaces to best effort.
- [ADR-0013](0013-release-v1-0-0-as-alpha-pre-releases.md): the train of
  1.0.0-alpha.N pre-releases, which began the same day.

Recorded retroactively in October 2026.
