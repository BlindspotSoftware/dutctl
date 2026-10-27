---
status: accepted
date: 2024-09-26
---

# Initialize every module once at agent start

## Context and problem statement

When dutagent loads its configuration, it creates one module instance for
each module entry ([ADR-0005](0005-configure-devices-and-commands-in-one-yaml-file.md)).
The `Module` interface had `Init` and `Deinit`. Its documentation said that
`Init` would run on each request for a command that uses the module, and
`Deinit` after the command. The agent called neither yet.

Modules need a place to check their configuration and prepare their
resources, and a place to release them again. The question was when the agent
calls these methods, and what it does when a module cannot initialize.

## Decision outcome

dutagent initializes every module once, when it starts, and deinitializes
every module once, when it stops.

- Each module entry in the configuration is one module instance. It lives as
  long as the agent and serves every run of its command.
- At start, dutagent calls `Init` on every module of every command, also
  after one has failed. It reports all failures together, and any failure
  aborts the start.
- `dutagent -dry-run` runs the same `Init` phase and exits without serving.
- `Deinit` runs for every module when the agent stops, and when the start
  fails after `Init`. It never runs at the end of a command.
- `Init` checks the module's configuration and prepares its resources;
  `Deinit` releases them.

### Consequences

- Good, because a broken configuration or a module that cannot work shows up
  at start or in a dry run, not in the middle of a user's command.
- Good, because the admin sees all failing modules at once.
- Bad, because one failing module keeps the agent from serving any of its
  devices; see [ADR-0021](0021-contain-module-faults-to-their-own-command.md) for what this means
  for `Init`.
- Bad, because all runs of a command share its module instances. State that
  a module keeps in its struct during `Run` carries over into the next run,
  and two runs of one command at the same time would share it. A device runs
  one command at a time ([ADR-0030](0030-run-one-command-at-a-time-per-device.md)), so
  this cannot happen today.
- Neutral, because `Deinit` does not run when the agent is killed. The next
  `Init` then finds the hardware as it was left.

### Confirmation

`cmds/dutagent/lifecycle_test.go` checks that `Init` and `Deinit` run for
every module even after one has failed, and that the failures are reported
together. The agent's exit code tests check that a failing `Init` fails a dry
run.

## More information

- [pkg/module/module.go](../../pkg/module/module.go): the contract of `Init`
  and `Deinit`.
- [cmds/dutagent/lifecycle.go](../../cmds/dutagent/lifecycle.go): how the
  agent runs them.
- [docs/dutagent-shutdown.md](../dutagent-shutdown.md#when-deinit-runs): when
  `Deinit` runs.
- [ADR-0004](0004-compile-modules-into-the-agent-behind-a-session.md): the module interface.
- [ADR-0027](0027-cancel-work-through-contexts-without-a-deadline-on-run.md): how contexts stop the
  work of modules.
- [ADR-0032](0032-stop-the-agent-in-stages.md): how the agent stops before it
  deinitializes the modules.
- Later changes:
  - 2026-03: `Deinit` had to be safe to call when `Init` never ran or failed
    partway. The agent called `Deinit` only if it attempted `Init`, and a
    panic in one module's `Init` or `Deinit` no longer stopped the others.
  - 2026-07: `Init` and `Deinit` began to receive a context. It carried the
    module's logger and ended after 5 minutes for `Init` and 15 seconds for
    `Deinit`.
  - 2026-10: `Deinit` began to run only once no command ran any more. After
    15 seconds the agent exited without waiting for a module whose `Deinit`
    had not returned. A stop signal during the abort stage made the agent
    exit at once without `Deinit`
    ([ADR-0032](0032-stop-the-agent-in-stages.md)).

Recorded retroactively in October 2026.
