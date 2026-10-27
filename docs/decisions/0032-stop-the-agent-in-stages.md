---
status: accepted
date: 2026-10-04
---

# Stop the agent in stages

## Context and problem statement

Operators update the software and configuration of DUT workers and restart
dutagent for it, for example with `systemctl restart --no-block`. Since July
2026, a stop signal made the agent drain in-flight requests for up to 15
seconds, then deinitialize the modules and exit. Commands still running had
their modules deinitialized under them, a CI job between two commands lost its
reservation and its device state, and a second signal was ignored.

[ADR-0031](0031-keep-a-device-busy-until-its-modules-return.md) had just made a
run end only once its modules have returned, so an empty locker now means that
no module runs. How should the agent stop without cutting work short, while an
admin can still end it quickly?

## Decision drivers

- A restart must not cut short a running command or a job's reservation.
- Deinit must never run beside a module: it may drive GPIO pins low or delete
  flash images.
- The admin escalates with signals alone; dutagent needs no service manager.
- No new deadline in the agent: a stage ends when its work is done, or with the
  admin's next signal.

## Considered options

- **Drain requests for a fixed time**: the behavior since July 2026, 15
  seconds for in-flight requests, then Deinit and exit.
- **Wait for the reservations, up to a drain timeout**: a `-drain-timeout` flag,
  by default 9 hours. Commands without a reservation get 15 seconds, and a
  second signal stops at once.
- **Three stages, each entered by a stop signal**: drain, abort and exit, with
  no timeout in the agent.

## Decision outcome

Chosen option: **three stages, each entered by a stop signal**, because it waits
for all work under way, reservations and commands alike. It never runs Deinit
beside a module, and it leaves each escalation to the admin instead of a clock.

- `SIGTERM`, `SIGINT` and `SIGHUP` are stop signals and count together.
  `SIGINT` and `SIGHUP` stay ignored if the agent started with them ignored, as
  under `nohup`. `SIGQUIT` is not caught, so Go dumps every goroutine's stack,
  which shows a module that does not return.
- Stage 1, drain, on the first signal: the agent grants no new reservation and
  runs commands only on devices the caller has reserved. The owner of a
  reservation may still extend and release it. The agent waits, without a time
  limit, until no device is busy or reserved.
- Stage 2, abort, on the next signal: all reservations end, and the running
  commands are cancelled; their clients get `Aborted`. The agent waits, without
  a time limit, until the modules have returned. A failure of the agent itself,
  such as the RPC service stopping on its own or a recovered panic, enters this
  stage directly.
- Stage 3, exit, on a signal during stage 2: the agent exits at once with
  status 1 and skips Deinit.
- Work refused while the agent stops gets `FailedPrecondition`, the code of a
  busy device, so a client can try again later. With `Unavailable`, dutctl
  would report an unreachable agent.
- Only after stage 1 or 2 does the agent stop the RPC service and run Deinit,
  bounded to 15 seconds. A module whose Deinit has not returned by then is left
  behind, and the agent exits with status 1.

The example systemd unit in `packaging/`, which the CI uses, leaves the stop to
dutagent; dutagent itself needs no service manager.

### Consequences

- Good, because a restart no longer cuts a command or a job short, and an idle
  agent still stops at once.
- Good, because Deinit never runs beside a module.
- Good, because the agent logs what it waits for, and the admin decides when to
  escalate.
- Bad, because a forgotten reservation or a stuck module holds up the stop until
  it ends or the admin sends the next signal. `dutctl <device> unlock force`
  ends a single forgotten reservation.
- Bad, because an exit in stage 3, or a kill, skips Deinit; only Init at the
  next start brings the hardware back to its initial state.

### Confirmation

Tests in `cmds/dutagent/shutdown_test.go` drive the stages with fake and real
signals. Tests in `internal/dutagent/locker/locker_test.go` cover the drain and
close modes, and `cmds/dutagent/lifecycle_test.go` the bound on Deinit.

## More information

- [docs/dutagent-shutdown.md](../dutagent-shutdown.md): every way a command or
  the agent ends, and when Deinit runs.
- [ADR-0009](0009-initialize-every-module-once-at-agent-start.md): the modules are
  initialized once at start and deinitialized at the end.
- [ADR-0017](0017-lock-devices-in-agent-memory.md): the reservations that stage 1
  waits for.
- [ADR-0027](0027-cancel-work-through-contexts-without-a-deadline-on-run.md): stage 2 cancels the runs
  through their contexts.
- [ADR-0031](0031-keep-a-device-busy-until-its-modules-return.md): a run ends
  only once its modules have returned.
- [cmds/dutagent/shutdown.go](../../cmds/dutagent/shutdown.go) holds the
  stages, and [internal/dutagent/locker](../../internal/dutagent/locker) the
  drain and close modes.

Recorded retroactively in October 2026.
