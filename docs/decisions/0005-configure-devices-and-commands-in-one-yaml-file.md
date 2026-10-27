---
status: accepted
date: 2024-08-20
---

# Configure devices and commands in one YAML file

## Context and problem statement

Every dutagent serves its own devices, wired in its own way. The agent needs
to know which devices it serves, which commands each device offers, and which
of the compiled-in modules ([ADR-0004](0004-compile-modules-into-the-agent-behind-a-session.md))
carry out a command, with which settings. A command is a task such as "flash
the firmware". It may take several hardware steps, for example toggling GPIOs
around an SPI flash write.

The documentation had carried a draft of the schema since March 2024. A draft
module guide from the same month let each module check a generic options map
itself, in a `ValidateOptions` method, against a struct with YAML and
`required` tags.

## Decision outcome

dutagent takes its whole setup from one YAML file, which it reads once when it
starts.

- The file names the devices that the agent serves. Each device has a
  description and named commands.
- A command has a description and an ordered list of module entries: the
  steps that carry it out.
- A module entry names a registered module and configures it with a map of
  options. dutagent decodes the map into the exported fields of the module's
  struct, matched by their lower-case names, so a module needs no YAML tags
  and no parsing code of its own. It then validates the struct with
  go-playground/validator, so a module can mark fields as required with
  struct tags.
- A module entry may carry fixed arguments. How the user's arguments reach
  the modules is the subject of
  [ADR-0016](0016-route-arguments-through-passthrough-or-templates.md).
- A file that does not decode or validate stops the agent at start. A change
  to the file takes effect with the next start.

### Consequences

- Good, because one file describes an agent's whole setup, and
  `dutagent -check-config` checks it without starting the agent.
- Good, because a module declares its options once, as plain struct fields.
- Bad, because the decoder ignores keys that it does not know: a misspelled
  option is dropped without a warning, as if it were not set.
- Bad, because a change takes a restart of the agent, which drops the
  reservations it keeps in memory
  ([ADR-0017](0017-lock-devices-in-agent-memory.md)).
- Neutral, because no module uses validator tags so far. The modules check
  their options in `Init`
  ([ADR-0009](0009-initialize-every-module-once-at-agent-start.md)).

### Confirmation

`pkg/dut/config_test.go` decodes valid and invalid files from
`pkg/dut/testdata`, and the agent's exit code tests run `-check-config` on a
valid and an invalid file.

## More information

- [docs/dutagent-config.md](../dutagent-config.md): the configuration grammar.
- [contrib/dutagent-cfg-example.yaml](../../contrib/dutagent-cfg-example.yaml):
  an example file.
- [pkg/dut/config.go](../../pkg/dut/config.go): decoding and validation.
- [ADR-0028](0028-tier-the-public-interfaces-for-v1-0-0.md): the configuration grammar
  is a primary public interface.
- Later changes:
  - 2024-09: dutagent began to run commands with more than one module. It
    runs them in order and stops at the first that fails; before, it refused
    such commands.
  - 2024-09: the `-c` flag names the file, and `-check-config` validates it
    without starting the agent.
  - 2024-11: fixed module arguments became a list of strings. Splitting one
    string at spaces broke quoted arguments; a shell-like lexer was left for
    later.
  - 2026-01: the keys `modules` and `options` became `uses` and `with`
    ([ADR-0015](0015-name-the-configuration-keys-after-github-actions.md)).
  - 2026-03: configuration errors name the device, the command and the YAML
    line.
  - 2026-03: the rule that exactly one main module per command receives the
    user's arguments was replaced
    ([ADR-0016](0016-route-arguments-through-passthrough-or-templates.md)).

Recorded retroactively in October 2026.
