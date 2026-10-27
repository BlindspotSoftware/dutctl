---
status: accepted
date: 2024-08-20
---

# Compile modules into the agent behind a Session

## Context and problem statement

dutagent runs on a single-board computer that is wired to the devices under
test. Each setup needs support for its own hardware: GPIOs, serial ports,
flash programmers, power switches. The agent needed a way to add such support
without changing its core. The code that drives the hardware also has to talk
to the user while a command runs: print status, read input, transfer files.

A draft module guide from March 2024 passed each module a `module.Context`.
It embedded `context.Context` and carried handlers for the console, files,
logging, printing and the client's terminal. The first draft of the agent
noted a similar idea, passing the I/O channels to the module through the
context, and set it aside.

## Considered options

- **A Session argument**: `Run` gets a context for cancellation and a separate
  `Session` interface for all exchange with the user.
- **Everything in the context**: a module context that carries the I/O
  handlers or channels next to cancellation, as in the March 2024 draft.

## Decision outcome

Chosen option: **a Session argument**, because the channels to the user are
then explicit. In the words of the first agent draft, they are passed
explicitly "for better understanding", and the context only cancels and
carries data that does not change during the run.

- A module is a Go package that implements `module.Module`: `Help`, `Init`,
  `Deinit` and `Run(ctx, session, args...)`. The project's modules live in
  `pkg/module/<name>`.
- The package registers itself from its `init` function with
  `module.Register`, giving an ID and a factory. dutagent links it in with a
  blank import, and `cmds/dutagent/modules.go` collects these imports. The
  set of modules is fixed when dutagent is built; nothing is loaded at run
  time. The configuration names a module by its ID
  ([ADR-0005](0005-configure-devices-and-commands-in-one-yaml-file.md)).
- A module reaches the user only through the `module.Session` passed to
  `Run`: the print functions, the console streams, and file requests and
  sends. It never sees the RPC stream.
- A module reports a failure by returning an error from `Run`. dutagent
  delivers the error to the client, so the module does not print it as well.

### Consequences

- Good, because support for new hardware is a new package and one import
  line. The RPC protocol and the client stay as they are, as long as the
  module needs nothing beyond the `Session`.
- Good, because modules depend only on the `Session` interface. They can be
  tested against a double ([internal/test/mock](../../internal/test/mock)),
  and the agent can change how it carries their I/O
  ([ADR-0007](0007-route-module-input-and-output-through-a-session.md)) without touching
  them.
- Good, because modules are plain Go code in one binary: the compiler checks
  them against the interface, and there are no plugins to install.
- Bad, because a new or changed module needs a rebuilt and redeployed
  dutagent. A module package that no blank import pulls in does not exist for
  the agent.
- Bad, because a change to the `Module` or `Session` interface breaks every
  module at compile time, as when `Init` and `Deinit` gained a context.

### Confirmation

`module.Register` panics at program start on an empty or duplicate ID or a
missing factory, and dutagent refuses a configuration that names a module
that is not registered. Reviews ask modules to return errors instead of
printing them.

## More information

- [docs/module_guide.md](../module_guide.md): how to write and register a
  module.
- [pkg/module/module.go](../../pkg/module/module.go): the `Module` and
  `Session` contract.
- [ADR-0009](0009-initialize-every-module-once-at-agent-start.md): when `Init` and
  `Deinit` run.
- [ADR-0021](0021-contain-module-faults-to-their-own-command.md): how the agent contains a failing
  module.
- Later changes:
  - 2025-07: the `Session` got `Printf` and `Println` next to `Print`, all
    with `fmt` semantics, and the client stopped adding a newline to printed
    text.
  - 2026-07: `Init` and `Deinit` began to receive a context.

Recorded retroactively in October 2026.
