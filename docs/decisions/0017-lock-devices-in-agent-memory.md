---
status: accepted
date: 2026-06-03
---

# Lock devices in agent memory

## Context and problem statement

The devices behind a dutagent are shared by developers and CI jobs. Nothing
coordinated their use: two users or jobs could send conflicting commands to one
device at once, such as a flash while someone else runs diagnostics. Nobody
could see who was using a device, or until when.

Users asked to reserve a device for a while, and to learn who holds it when
they are turned away. Where should the lock state live, and what should the
agent enforce?

## Decision drivers

- Users see who holds a device and for how long.
- No new dependency or setup: agents may run on single-board computers.
- Most deployments connect the client directly to an agent; the dutserver
  relay is experimental.
- A long operation, such as a flash, must not lose its device midway.

## Considered options

- **In memory in each agent**: a map behind a mutex, lost when the agent ends.
- **Lock files on the agent**: state that survives a restart.
- **A distributed store**: etcd, Redis or SQLite, shared by all agents.
- **Locks on the dutserver relay**: one place for all agents behind it.

## Decision outcome

Chosen option: **in memory in each agent**, because it is the simplest correct
solution: no dependency, no configuration and no file I/O that can fail. Lock
files bring stale-file cleanup and races, a store brings operations work, and
the relay is optional.

- Each agent keeps the locks of its devices in memory. They are gone when the
  agent ends; a reservation covers a stretch of active work, whose owner will
  likely notice a restart.
- A user reserves a device with `lock` for a duration and releases it with
  `unlock`; locking again extends the reservation. While it lasts, only the
  holder's commands run on the device.
- While a command runs, the agent holds its device automatically. This hold
  has no expiry and ends with the command. A one-hour safety-net expiry and a
  short expiry renewed by client heartbeats were weighed; heartbeats change the
  protocol and can lose the device during a long flash.
- A run or a reservation on a device that another user holds is refused with
  `FailedPrecondition`, naming the holder.
- Locks coordinate users who cooperate; they are no security boundary. Callers
  name themselves ([ADR-0018](0018-identify-callers-by-the-http-from-header.md)), so
  anyone can claim another user's name, and any caller may force the release
  of another user's lock.
- Lock and Unlock are unary RPCs on `DeviceService`, rather than a service of
  their own or messages on the Run stream. List returns a `DeviceInfo` for each
  device, with its lock state: a breaking change, chosen over adding a parallel
  list of lock records.
- `lock` and `unlock` are reserved command names; a configuration that uses
  them fails to load.

### Consequences

- Good, because locking needs no setup, and a refused user learns who holds
  the device.
- Bad, because a restart of the agent drops every reservation.
- Bad, because a forgotten reservation blocks the device until it expires or
  someone forces its release.
- Bad, because the new List response is a breaking change for older clients.
- Neutral, because the dutserver relay keeps no locks: it answers Lock and
  Unlock with `Unimplemented` and forwards the caller's name on Run.

### Confirmation

Tests in `internal/dutagent/locker/locker_test.go` and
`cmds/dutagent/rpc_test.go` cover reservations, their expiry and extension,
forced releases and the lock state in List.

## More information

- [ADR-0018](0018-identify-callers-by-the-http-from-header.md): how lock holders are
  named.
- [ADR-0026](0026-defer-authentication-to-mtls.md): why callers are not
  authenticated yet, so that the locks rely on cooperation.
- [ADR-0030](0030-run-one-command-at-a-time-per-device.md) and
  [ADR-0031](0031-keep-a-device-busy-until-its-modules-return.md): what the hold
  of a running command admits, how long it lasts, and why a forced unlock no
  longer ends it.
- [ADR-0032](0032-stop-the-agent-in-stages.md): a stopping agent waits for the
  reservations to end.
- [internal/dutagent/locker](../../internal/dutagent/locker) keeps the locks;
  [docs/dutagent-shutdown.md](../dutagent-shutdown.md) shows how they end.
- Later changes:
  - 2026-07: a Lock request without a duration got the agent's default of 30
    minutes, and the client began to warn about durations above 8 hours. By
    design, locks have no maximum.
  - 2026-07: the locker began to model each hold by kind, reserved or busy,
    and List to show a device that runs a command as in use.
  - 2026-07: Lock and a normal Unlock began to require a named caller. A
    caller without a name could still run commands and force a release.

Recorded retroactively in October 2026.
