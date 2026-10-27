---
status: accepted
date: 2026-10-27
---

# Add the module API to the primary public interfaces

## Context and problem statement

[ADR-0028](0028-tier-the-public-interfaces-for-v1-0-0.md) split the public interfaces
into two tiers. Its primary tier, the configuration grammar and the RPC
protocol, becomes stable and backwards compatible with v1.0.0. Neither tier
named a Go package, so the module API in `pkg/module` had no stated promise.

Yet modules are how users add support for their hardware. The README invites
them to write their own, and the [module guide](../module_guide.md) explains
the interfaces in `pkg/module` that every module implements and uses. A module
is a Go package compiled into dutagent
([ADR-0004](0004-compile-modules-into-the-agent-behind-a-session.md)); one kept in another
repository imports `pkg/module` like any Go library.

Go's module rules apply as well. The `v1.0.0-alpha.N` pre-releases promise no
compatibility. From v1.0.0 on, a package must stay backwards compatible under
its import path, and a breaking change needs v2.0.0 with the module path
`github.com/BlindspotSoftware/dutctl/v2`. This holds for every package that
other Go modules can import: all packages outside `internal/` and `cmds/`.

Should the module API be part of the compatibility promise?

## Considered options

- **Primary tier**: the module API becomes stable and backwards compatible
  with v1.0.0, like the configuration grammar and the RPC protocol.
- **Not mentioned**: the public interfaces keep naming no Go package.

## Decision outcome

Chosen option: **primary tier**, because modules are how users extend
dutagent, and from v1.0.0 on Go's rules expect `pkg/module` to stay compatible
anyway.

- The primary tier now has three parts: the agent configuration grammar, the
  RPC protocol and the module API. All three become stable and backwards
  compatible with v1.0.0.
- The module API is the package [pkg/module](../../pkg/module): the `Module`
  interface that a module implements, the `Session` through which it reaches
  the user, and `Record` and `Register`, with which it registers. The promise
  covers every exported name in the package and the behavior that its doc
  comments state.
- Time limits are not promised as numbers. A module may rely on the contexts
  of `Init` and `Deinit` carrying a deadline, and on the context of `Init`
  also ending when the agent is asked to stop during startup, but not on how
  long the agent waits; that is its current behavior and may change.
- Before v1.0.0, the doc comments are reviewed, so that they state only what
  a module can rely on and mark the agent's current behavior as such.
- Until v1.0.0, the module API may still change like the other primary
  interfaces; the release notes announce the changes.
- The [README](../../README.md#public-interfaces) lists it as the third
  primary interface. The rest of ADR-0028 stays as it is.

### Consequences

- Good, because a module written for v1.0.0 keeps building and working with
  every later 1.x release.
- Bad, because from v1.0.0 on, a change that breaks modules needs v2.0.0 and a
  new module path for the whole repository. Adding a method to `Module` or
  `Session` is such a change: it breaks every type that implements the
  interface, test doubles included.
- Bad, because dutctl, dutagent and dutserver share one version. After such a
  change, a 2.x agent rejects every 1.x client
  ([ADR-0023](0023-reject-rpcs-from-another-major-version.md)), even if the RPC protocol
  stayed the same.
- Bad, because what the project's modules take from `internal/` stays outside
  the promise, and modules in other repositories cannot import it:
  `internal/log` for the logger
  ([ADR-0022](0022-log-through-a-scoped-logger-in-the-context.md)),
  `internal/procexec` for external tools, and `internal/test/mock` for a
  `Session` double in tests.
- Neutral, because this ADR covers only `pkg/module`. From v1.0.0 on, Go's
  rules apply as well to `pkg/dut`, `pkg/headers`, the module packages under
  `pkg/module/` and the generated code under `protobuf/gen/`.

### Confirmation

Reviewers ask for an ADR when a pull request changes a primary interface (see
[README.md](README.md)). No CI job compares the Go API between releases yet.

## More information

- [ADR-0028](0028-tier-the-public-interfaces-for-v1-0-0.md): the two tiers and the road
  to v1.0.0.
- [ADR-0004](0004-compile-modules-into-the-agent-behind-a-session.md): how modules plug into
  dutagent.
- [docs/module_guide.md](../module_guide.md) and
  [pkg/module/module.go](../../pkg/module/module.go): the module contract.
- The Go Modules Reference on [versions](https://go.dev/ref/mod#versions) and
  [major version suffixes](https://go.dev/ref/mod#major-version-suffixes), and
  [Keeping Your Modules Compatible](https://go.dev/blog/module-compatibility).
