---
status: accepted
date: 2026-03-31
---

# Route arguments through passthrough or templates

## Context and problem statement

Every command had exactly one main module. It received the runtime
arguments, the words after the command on the dutctl command line
([ADR-0010](0010-use-a-positional-cli-grammar-with-reserved-keywords.md)), verbatim, and its help
was the command's help. The only module of a command was its main module
implicitly; the other modules got fixed `args` from the configuration
([ADR-0005](0005-configure-devices-and-commands-in-one-yaml-file.md)).

This left two gaps. There was no clean way to pass only some of the arguments
to a module, or to use one argument in several modules. And a command that
needs no input still had to name a main module. In December 2025 a
contributor proposed command-level templating in the Design Proposals
discussions.

## Considered options

- **Named arguments and a passthrough flag**: the command declares named
  arguments, which module `args` reference as `${name}`; alternatively, one
  module is flagged to receive all arguments.
- **Template variables for forwarding**: `${args}` expands to all arguments
  and `${remaining}` to those left after the named ones.
- **YAML anchors**: reuse values within the configuration file.
- **A template engine**: Go's `text/template` in the agent, or a tool such
  as Jinja2 that renders the file before the agent reads it.

## Decision outcome

Chosen option: **named arguments and a passthrough flag**, because a flag on
the module is explicit and needs no implicit variables or reserved words.
Anchors and an external tool work on the file once and never see the
arguments that a user passes at run time. `text/template` would impose its
`{{.name}}` syntax, and Jinja2 in the agent a Python dependency, for a need
that a plain `${name}` substitution covers.

A command handles runtime arguments in one of three ways:

- **Static**: it takes none. Every module gets its fixed `args`.
- **Passthrough**: at most one module is flagged `passthrough: true` (called
  `main` until May 2026). It receives all runtime arguments unchanged, must
  not have fixed `args`, and its help is the command's help. Other modules
  keep their fixed `args`.
- **Templating**: the command declares named `args`, each with an optional
  description. Runtime arguments map to them by position and must match
  their number. Module `args` reference them as `${name}`.

A command cannot have both a passthrough module and declared arguments. A
reference to an undeclared name fails when the configuration loads. Runtime
arguments for a command with neither are refused before any module runs.

### Consequences

- Good, because a command can hand one value to several modules, and a
  command without input needs no main module.
- Good, because declared arguments can carry descriptions, which the
  command's help lists.
- Bad, because templated arguments match by position: reordering the
  declarations changes which value each reference gets.
- Neutral, because the discussion ended with a forwarding flag that also
  takes the arguments that templating leaves over. The implementation keeps
  templating and passthrough apart, as the original proposal did, so a
  command cannot template some arguments and forward the rest.

## More information

- [docs/command-arg-templating.md](../command-arg-templating.md): the three
  ways, with examples.
- [pkg/dut/dut.go](../../pkg/dut/dut.go) and
  [pkg/dut/templating.go](../../pkg/dut/templating.go): the rules in code,
  starting at `Command.ModuleArgs`.
- [ADR-0015](0015-name-the-configuration-keys-after-github-actions.md): the names of the
  other keys.
- Later changes:
  - 2026-05: the flag `main` was renamed to `passthrough`, because "main"
    suggested rank rather than its role of routing the arguments. Existing
    files had to change.
  - 2026-06: the agent began to accept runtime arguments for a command with
    declared arguments; until then it refused them because the command had no
    passthrough module.

Recorded retroactively in October 2026.
