---
status: accepted
date: 2026-10-02
---

# Keep a device busy until its modules return

## Context and problem statement

A Run request starts the command's modules, which drive the hardware, and
broker workers, which carry the modules' I/O over the stream
([ADR-0007](0007-route-module-input-and-output-through-a-session.md)). The handler could
return while either was still active. In September 2026, a broker worker still
inside a stream send after the handler had returned crashed the whole agent;
since then, the handler waits for its workers.

The modules ran on a goroutine nobody joined. When the client hung up, the
broker failed or a deadline passed, the handler released the device while a
module still drove it. Another run could start on it, and a client could even
provoke this by sending a file nobody requested.

A module that honors its context may still need time to stop: a flash tool
gets a grace period after `SIGTERM`. How long does a run keep its device, and
what may end that early?

## Decision drivers

- A device is never handed on while a module still drives it.
- Nothing writes to the stream after the Run handler has returned.
- When a device stays busy, the admin can see why.

## Considered options

- **Wait without a time limit**: return only after the modules and the broker
  workers have stopped, and warn while a cancelled module lingers.
- **Give up after a timeout**: abandon a broker worker still stuck in a send
  after a grace period.
- **Release the device when the handler returns**: as before, while a module
  may still drive it.
- **Let a forced unlock end the run**: `unlock force` frees a busy device, as
  it did before, or even cancels its command.

## Decision outcome

Chosen option: **wait without a time limit**, because each shortcut brings back
a hazard. An abandoned send races the handler's teardown, and a device released
early is driven by a module and the next run at once.

A forced unlock is no way out either: it exists to break a reservation someone
forgot. When it freed a busy device, the old command's late release could end a
newer run's hold.

- The modules run on the Run handler's goroutine. The handler returns, and the
  device's busy hold ends, only after the modules and the broker workers have
  stopped.
- No timer cuts this wait short. While a cancelled module has not returned, the
  agent logs a warning every 10 seconds, and once it returns, how long it took.
- Only the run that took a busy hold ends it. `unlock force` releases a
  reservation, never a running command's hold; on a device that only runs a
  command, it fails with `FailedPrecondition`.
- A module's `Run` must return promptly once its context is cancelled. There is
  no command to end a stuck run; one was left for a later decision.

### Consequences

- Good, because no other run, the same user's included, starts on a device
  while a module still drives it.
- Good, because a device that is not busy has no module running, which the
  staged stop of the agent builds on
  ([ADR-0032](0032-stop-the-agent-in-stages.md)).
- Bad, because a retry right after Ctrl-C is refused until the cancelled module
  has returned.
- Bad, because a module that ignores its context keeps its device busy until it
  returns, or until the admin stops the agent.
- Bad, because a client that stops reading keeps the device busy. A lost
  connection keeps it busy until the agent notices: after about 2.5 minutes,
  or up to about 15 minutes with data in flight.

### Confirmation

In `cmds/dutagent/run_test.go`, `TestRunHoldsDeviceUntilModulesReturn`,
`TestRunRetryWhileCancelledModuleStops`, `TestRunForcedUnlockKeepsDeviceBusy`
and `TestWarnUntilReturned` keep the rule, and so does
`TestForceClearLockLeavesBusyHold` in `internal/dutagent/locker/locker_test.go`.

## More information

- [ADR-0017](0017-lock-devices-in-agent-memory.md): the busy hold of a running
  command.
- [ADR-0027](0027-cancel-work-through-contexts-without-a-deadline-on-run.md): cancellation reaches a
  module through its context.
- [ADR-0030](0030-run-one-command-at-a-time-per-device.md): a device runs one command at
  a time.
- [ADR-0032](0032-stop-the-agent-in-stages.md): the stop stages wait for the
  modules as well.
- [docs/dutagent-shutdown.md](../dutagent-shutdown.md) lists how a command ends
  and what that means for its device. [docs/module_guide.md](../module_guide.md)
  and [pkg/module/module.go](../../pkg/module/module.go) state the module
  contract.

Recorded retroactively in October 2026.
