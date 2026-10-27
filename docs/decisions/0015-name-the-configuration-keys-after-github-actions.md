---
status: accepted
date: 2026-01-21
---

# Name the configuration keys after GitHub Actions

## Context and problem statement

In the agent configuration
([ADR-0005](0005-configure-devices-and-commands-in-one-yaml-file.md)), a command listed
its steps under `modules`. A module entry took its settings under `options`
and its fixed arguments under `args`. In July 2025 the maintainers asked in
the Design Proposals discussions whether the keys should follow a scheme that
users already know, such as GitHub Actions with its jobs, steps, `uses` and
`with`. Any rename would break every existing configuration file.

## Considered options

- **Keep `modules` and `options`**: existing files stay valid.
- **`uses` for `modules` and `with` for `args`**: the first concrete
  suggestion.
- **`runs`, `stages` or `steps` for `modules`**: other words for steps that
  run one after another.
- **`uses` for `modules` and `with` for `options`, keep `args`**.

## Decision outcome

Chosen option: **`uses` for `modules` and `with` for `options`, keep
`args`**, because the configuration then reads like a GitHub Actions
workflow, where a step `uses` an action and passes it settings `with` a map.
`args` keeps its name because dutctl should resemble command-line interaction
and sometimes hands the arguments on to a command; no other name fit better.

- A command lists its modules under `uses`. A module entry takes its options
  under `with`.
- `args` and the other keys, such as `desc`, `cmds` and `module`, keep their
  names.
- The old keys are no longer supported, and there are no aliases for them.
- The Go types keep their field names: `Command.Modules` and
  `ModuleConfig.Options` map to the new keys through YAML tags.

### Consequences

- Good, because the keys follow a workflow syntax that many users know.
- Bad, because every existing configuration had to be changed.
- Bad, because the agent does not point at the rename. A file that still
  says `modules` fails with "command must have at least one module", and an
  entry that still says `options` loses its options without a warning.
- Neutral, because Go code and YAML use different names for the same fields.

## More information

- [docs/dutagent-config.md](../dutagent-config.md): the configuration grammar.
- [pkg/dut/dut.go](../../pkg/dut/dut.go): the types and their YAML tags.
- [ADR-0016](0016-route-arguments-through-passthrough-or-templates.md): how
  runtime arguments reach the modules, including the later rename of the key
  `main` to `passthrough`.
- [ADR-0028](0028-tier-the-public-interfaces-for-v1-0-0.md): the configuration grammar
  is a primary public interface.

Recorded retroactively in October 2026.
