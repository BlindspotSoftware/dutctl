---
status: accepted
date: 2026-10-02
---

# Run one command at a time per device

## Context and problem statement

Since device locks came in ([ADR-0017](0017-lock-devices-in-agent-memory.md)), a
device that ran a command admitted further runs by the same owner, and they
shared its one busy hold. The first of them to end released the hold and left
the device free to other users, while the owner's other run still drove it.

The owner is the caller's name, by default `<user>@<host>`
([ADR-0018](0018-identify-callers-by-the-http-from-header.md)). So all terminals and
scripts of one user on one machine are one owner: a console in one terminal and
a power cycle from another shared a hold this way.

[ADR-0031](0031-keep-a-device-busy-until-its-modules-return.md) made a run keep
its device until its modules have returned, which would turn this into a
routine fault: every quick retry after Ctrl-C would hit it. May a user start a
second command on a device that already runs one of theirs?

## Considered options

- **Refuse a second run, whoever asks**: one command at a time per device.
- **Count the owner's runs on the shared hold**: admit them, and end the hold
  with the owner's last run. A generation number keeps a late release from
  ending a newer hold.

## Decision outcome

Chosen option: **refuse a second run, whoever asks**, because a second command
of the same user would use the device twice. Counting would end the early
release, but keep the double use and add a counter and a generation to the
locker.

A device runs one command at a time. While it runs one, the agent refuses
another run on it with `FailedPrecondition`. Another user gets the usual
"locked by" error, and the user whose command runs gets:

```text
device "dev1" is still running a command for "alice@host"; a cancelled command keeps the device until it has stopped
```

The user's own reservation of the device does not stand in the way of a run.
No two runs share a hold, and a forced unlock leaves it in place
([ADR-0031](0031-keep-a-device-busy-until-its-modules-return.md)), so only the
run that took a hold ends it. The rule holds however callers are identified.
Steps that belong together, such as a power cycle followed by watching the
serial console, are configured as one command whose modules run in turn.

### Consequences

- Good, because no run can free a device while another one still drives it.
- Good, because the locker keeps one busy hold per device and releases it by
  device and owner, without a counter or an identity per run.
- Bad, because a user can no longer power-cycle a device from a second terminal
  while their own console on it is open. The maintainers accepted this.
- Bad, because a retry right after Ctrl-C is refused until the cancelled
  command has stopped.

### Confirmation

`TestAutoLockRejectsSameOwnerWhileBusy` in
`internal/dutagent/locker/locker_test.go` and
`TestRunSameUserSecondRunRejected` in `cmds/dutagent/run_test.go` keep the
rule.

## More information

- [ADR-0017](0017-lock-devices-in-agent-memory.md): the locks and the busy hold.
- [ADR-0029](0029-run-the-run-rpc-as-straight-line-code.md): the Run handler
  that applies the rule.
- [ADR-0031](0031-keep-a-device-busy-until-its-modules-return.md): a run keeps
  its device until its modules have returned, and a forced unlock does not end
  it.
- The dutctl usage text and the monitor mode section of the
  [serial module's README](../../pkg/module/serial/README.md) tell users to
  configure steps that belong together as one command.

Recorded retroactively in October 2026.
