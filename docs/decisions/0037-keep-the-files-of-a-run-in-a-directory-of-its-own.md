---
status: accepted
date: 2026-11-10
---

# Keep the files of a run in a directory of its own

## Context and problem statement

Some modules need files on the agent while they run, such as a firmware image
received from the client or read from a chip. Runs overlap: the agent runs
commands of different devices at the same time, and a user's different commands
of one device side by side
([ADR-0036](0036-run-a-users-different-commands-side-by-side.md)). Several
commands use the same module type, and a command's module instances serve
every run of the command
([ADR-0009](0009-initialize-every-module-once-at-agent-start.md)).

The module contract asked for per-run state in `Run`'s locals but said nothing
about files. The `flash` module kept every run's image in one fixed file,
`./image` in the agent's working directory, so two overlapping flash runs could
flash each other's image or a truncated one. `flash-emulate` kept its image
until the instance's next run, and the dummy file transfer module never removed
its files.

Where does a module keep the files of a run, and who removes them?

## Considered options

- **A directory per run under the temporary directory**: made with
  `os.MkdirTemp` under `$TMPDIR`, removed when `Run` returns.
- **A directory per run under the working directory**: next to the agent's
  configuration and state.
- **A module option for the location**: each command names where its module
  keeps files.
- **A file per run**: made with `os.CreateTemp`, without a directory.

## Decision outcome

Chosen option: **a directory per run under the temporary directory**, because
it needs only the standard library and no configuration, and keeps scratch
files apart from the agent's state.

The working directory would mix scratch files with state that modules keep on
purpose, and leftovers would pile up there. A module option would repeat, per
command, what `TMPDIR` already sets for the whole agent. A file per run works
for one file, but a run with several files would need a removal for each; a
directory takes them all with one.

- A module that needs files for a run creates a directory with
  `os.MkdirTemp("", "<module>-")` in `Run`, keeps the run's files in it, and
  removes it when `Run` returns, also after a failure, a cancellation or a
  panic. The path is a local of `Run`, like all per-run state.
- A module closes every file it opens, and treats a failed close of a file it
  wrote as a failed write: the data may not have reached the disk in full.
- The temporary directory is `$TMPDIR`, or `/tmp` if it is unset. An admin
  moves it for the whole agent with `TMPDIR`. It must exist; modules do not
  create it. The example systemd unit points it at `/var/lib/dutagent/tmp`,
  which the example tmpfiles configuration creates.
- The rule covers a run's scratch files only. A destination that the user or
  the configuration names, as with the `file` module, and state that a module
  keeps across runs on purpose are not scratch files.
- The doc comment of `Run` in [pkg/module](../../pkg/module/module.go) states
  the rule for module authors.

### Consequences

- Good, because overlapping runs never see each other's files, whichever
  modules and devices they belong to.
- Good, because a run cleans up after itself: `Deinit` has nothing to remove,
  and a run that needs further files gets them removed with the same call.
- Bad, because the temporary directory needs room for the files of all runs at
  once, firmware images of tens of MiB each; on a tmpfs, as `/tmp` is on many
  distributions, that room is RAM.
- Bad, because an agent that ends before a run returns, for instance when it is
  killed, leaves the run's directory behind until an admin removes it.

## More information

- [ADR-0036](0036-run-a-users-different-commands-side-by-side.md) and
  [ADR-0009](0009-initialize-every-module-once-at-agent-start.md): why runs of
  one module type overlap, and why a module instance serves many runs.
- [ADR-0033](0033-add-the-module-api-to-the-primary-public-interfaces.md): the
  behavior the module API's doc comments state is part of the public
  interfaces, and so is this rule.
- [ADR-0027](0027-cancel-work-through-contexts-without-a-deadline-on-run.md):
  a cancelled run stops its tools and returns, and so removes its directory.
- [pkg/module/flash](../../pkg/module/flash) and
  [pkg/module/flash-emulate](../../pkg/module/flash-emulate) follow the rule;
  [packaging/dutagent.service](../../packaging/dutagent.service) and
  [packaging/dutagent.tmpfiles](../../packaging/dutagent.tmpfiles) are the
  example unit and its temporary directory.
