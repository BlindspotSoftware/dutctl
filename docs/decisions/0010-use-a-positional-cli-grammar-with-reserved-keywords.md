---
status: accepted
date: 2024-09-26
---

# Use a positional CLI grammar with reserved keywords

## Context and problem statement

In September 2024 the protocol had the List, Commands and Run RPCs, and a
Details RPC was added for further information about a device or a command.
dutctl was still a demo that called them with hard-coded values. It needed a
command line for these calls that knows no device or command names: the agent's
configuration defines them, and the client learns them on request.

A few words would have a fixed meaning, such as `help`. When the grammar was
introduced, the maintainers noted that such words must not collide with names
in the agent configuration, must be named in the module documentation because a
module can never receive them as its first argument, and should be kept in one
place in the code.

## Decision outcome

dutctl reads its options with Go's standard `flag` package and treats the words
after them as positions, without a verb such as `run`:

```text
dutctl [options] [list]
dutctl [options] <device>
dutctl [options] <device> <command> [args...]
dutctl [options] <device> <command> help
dutctl [options] <device> lock [duration]
dutctl [options] <device> unlock [force]
dutctl version
```

- The first word names a device, the second a command on that device. The
  words after the command go to the command unchanged.
- A keyword is reserved only in the position where it would be ambiguous:
  `list` and `version` as a device, `lock`, `unlock` and `help` as a command,
  `help` as the first word after a command, where it must stand alone, and
  `force` only after `unlock`. A device may still be called `lock`, and a
  command `list`.
- The agent refuses a configuration that names a device or command like a
  keyword of that position, and dutctl never passes `help` to a module as the
  first argument. The reserved words live in
  [internal/keyword](../../internal/keyword), which the client's dispatch, the
  agent's Details handler and the configuration check all use.
- `help` calls the Details RPC with the keyword `help`. The keyword field lets
  the agent offer other kinds of information later without a new RPC; today it
  answers `help` only and rejects other keywords.

The first grammar had the `list`, device, command and `help` forms. The other
forms and the configuration check followed later (see More information).

### Consequences

- Good, because the common calls are short, and a bare `dutctl` lists the
  devices.
- Good, because arguments for the command may start with a dash: flag parsing
  stops at the first positional word.
- Good, because a new kind of information needs a new keyword, not a new RPC.
- Bad, because options must come before the device: an option written after it
  is a positional word. `dutctl <device> unlock --force` was once silently
  treated as a plain unlock; it is now rejected.
- Bad, because a new keyword in the device or command position takes that name
  away from devices or commands, and configurations that use it stop loading.

### Confirmation

`TestDispatch` in [cmds/dutctl/dutctl_test.go](../../cmds/dutctl/dutctl_test.go)
covers the `list`, device, `help`, `lock` and `unlock` forms and the
rejection of extra words. The tests of
[internal/keyword](../../internal/keyword) and the reserved-name cases in
[pkg/dut/config_test.go](../../pkg/dut/config_test.go) pin which names are
reserved in which position.

## More information

- [ADR-0016](0016-route-arguments-through-passthrough-or-templates.md): how the
  words after the command reach the modules.
- [ADR-0017](0017-lock-devices-in-agent-memory.md): the locks behind `lock` and
  `unlock`.
- [ADR-0028](0028-tier-the-public-interfaces-for-v1-0-0.md): how stable the command line
  is.
- Later changes:
  - 2025-08: `dutctl version` replaced the `-v` flag for version information,
    and `-v` turned on verbose output.
  - 2026-06: `lock [duration]` and `unlock` joined the grammar, with a global
    `-force` flag. The agent began to reject commands named `lock` or
    `unlock`.
  - 2026-07: a bare `dutctl` began to run `list`.
  - 2026-07: extra words after `lock`, `unlock` and `help` became an error.
    `force` became a word after `unlock` instead of the `-force` flag, a
    breaking change. `internal/keyword` became the single list of reserved
    words, and the agent began to reject devices named `list` or `version` and
    commands named `help` as well.

Recorded retroactively in October 2026.
