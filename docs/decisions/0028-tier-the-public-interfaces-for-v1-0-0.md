---
status: accepted
date: 2026-08-24
---

# Tier the public interfaces for v1.0.0

## Context and problem statement

With the first 1.0.0 pre-release in July 2025, the README named three public
interfaces that would become stable with the first major release
([ADR-0012](0012-make-the-clis-config-and-rpc-stable-with-v1-0-0.md)): the
command-line interfaces of dutctl, dutagent and the relay server, the agent
configuration, and the RPC protocol. No further release followed for more than
a year, until a contributor asked for a release and a predictable schedule.

Before promising backwards compatibility, the maintainers wanted to check in
real use that these were the right interfaces. They also had to say which
interfaces the promise covers. They had described the client as the tool for
people, and the agent's RPC API as the interface for other systems.

## Decision drivers

- Validate the interfaces in real use before promising compatibility.
- Release regularly and predictably.
- Harden the smaller surface, automated use, first.

## Decision outcome

The public interfaces fall into two tiers, which supersede the single list of
July 2025 ([ADR-0012](0012-make-the-clis-config-and-rpc-stable-with-v1-0-0.md)), and
the road to v1.0.0 has two pre-release phases. The
[README](../../README.md#release-plan) states both.

- Primary, the API contract: the agent
  [configuration grammar](../dutagent-config.md) and the
  [RPC protocol](../../protobuf/dutctl/v1/dutctl.proto). They become stable and
  backwards compatible with v1.0.0.
- Secondary, best effort: the command-line interfaces of dutctl, dutagent and
  dutserver. They are kept stable where possible, but the compatibility promise
  does not cover them.
- Alpha (`v1.0.0-alpha.N`, current) focuses on automated use: a CI team
  stresses the features for test pipelines and scripted workflows.
- Beta (`v1.0.0-beta.N`) adds daily interactive use by a team of firmware
  developers, once automated use proves stable.
- During both phases, releases come monthly at the latest, earlier when major
  features, fixes or interface changes land. The interfaces may still change
  in response to testing feedback; the release notes announce the changes.

### Consequences

- Good, because those who write agent configurations or call the RPC API know
  what stays compatible from v1.0.0.
- Good, because the command-line interfaces can still change where needed.
- Bad, because scripts that drive the command-line tools get best effort only;
  the compatibility promise covers the RPC API.
- Bad, because the interfaces may still change until v1.0.0, so users have to
  follow the release notes.
- Neutral, because this decision makes no promise for Go packages, not even
  for the module API in `pkg/module`.

### Confirmation

Reviewers ask for an ADR when a pull request changes a primary interface (see
[README.md](README.md)). `protobuf/buf.yaml` configures buf's breaking-change
rules, but no CI job runs them yet.

## More information

- Supersedes [ADR-0012](0012-make-the-clis-config-and-rpc-stable-with-v1-0-0.md).
- [ADR-0008](0008-release-from-conventional-commits-with-release-please.md): how breaking changes
  reach the release notes.
- [ADR-0013](0013-release-v1-0-0-as-alpha-pre-releases.md): the train of
  1.0.0-alpha.N pre-releases.
- [ADR-0023](0023-reject-rpcs-from-another-major-version.md): the agent rejects a client
  of another major version.
- [ADR-0033](0033-add-the-module-api-to-the-primary-public-interfaces.md): adds the
  module API in `pkg/module` to the primary tier.

Recorded retroactively in October 2026.
