---
status: accepted
date: 2026-10-02
---

# Run the Run RPC as straight-line code

## Context and problem statement

Since September 2024 the agent's Run handler drove a generic state machine
([ADR-0006](0006-drive-the-run-handler-with-a-generic-state-machine.md)). By September 2026 it had
six states: receive the command, find it, check device access, take the
device's auto-lock, start the modules and wait for them. Each state had
exactly one successor, and the handler was the machine's only user.

The machine added cost but no behavior. Its states passed dependencies,
intermediate results and channels on in one argument value, copied from state
to state. Cleanup could not live in a state, so the auto-lock release and the
broker's lifetime sat in the handler, behind a pointer record and defers
registered up front.

Two fixes in 2026 had to work around this. One stopped a panic from leaking
the lock; the other stopped a worker from writing to the stream after the
handler had returned, which crashed the agent. The split into states also hid
lifetime bugs, and the handler as a whole had no tests:

- The goroutine that ran the modules was never joined. On a client hang-up, a
  broker failure or a deadline, the handler returned and freed the device
  while a module still drove it.
- The wait loop spun on a closed channel while modules outlived the broker.
- Removing the handler's deferred wait for the broker's workers, or its
  deferred lock release, failed no test.

## Considered options

- **Keep the state machine**: fix the lifetimes in the states and the
  handler, as the two earlier fixes had done.
- **Straight-line code**: plain calls with early returns, and each release
  deferred where its resource is acquired.

## Decision outcome

Chosen option: **straight-line code**, because the machine had one user and
no branches, and its split into states made the lifetimes of a request hard to
follow and hid bugs.

- `Run` is the connect adapter: it looks up the caller, sets up logging and
  calls `run(ctx, stream, user)`, which takes a `session.Stream`. `run` is the
  seam where end-to-end tests drive a whole request through a fake client.
- `run` receives the command, finds it, resolves its arguments, takes the
  device's busy hold and calls `runInSession`, which starts the broker and
  calls `runModules`. Each step returns early on failure.
- Each resource's release is deferred right after it is acquired, so it runs
  on every exit path, a panic included.
- The modules run on the handler goroutine, each through `catchPanic`, the
  recovery that `Init` and `Deinit` use.
- There is no separate access check: `Locker.AutoLock` checks access and takes
  the busy hold in one step under the locker's mutex. `Locker.CheckAccess`,
  left without a caller, was deleted, and so was `internal/fsm`.
- The arguments are resolved before the hold is taken, not after it as in the
  state machine. A request that cannot run never makes the device busy, not
  even briefly, and reports its own fault, `InvalidArgument`, also on a device
  that is held or busy.

### Consequences

- Good, because the steps of a run and the lifetime of each resource read top
  to bottom in one file.
- Good, because the end-to-end tests fail when the deferred lock release or
  the wait for the workers is removed.
- Good, because the handler returns only once its modules have, which the
  rules of [ADR-0031](0031-keep-a-device-busy-until-its-modules-return.md)
  build on.
- Neutral, because a request with bad arguments on a device that another user
  holds now gets `InvalidArgument` instead of `FailedPrecondition`.

### Confirmation

[cmds/dutagent/run_test.go](../../cmds/dutagent/run_test.go) drives `run`
through a fake client stream. It checks what reaches the client, the status
codes, and that the device is busy while modules run and free afterwards. It
also checks that the arguments are resolved before locking, and that no
stream send outlives the run.

## More information

- Supersedes [ADR-0006](0006-drive-the-run-handler-with-a-generic-state-machine.md).
- [ADR-0030](0030-run-one-command-at-a-time-per-device.md) and
  [ADR-0031](0031-keep-a-device-busy-until-its-modules-return.md) record the
  lock and lifetime rules that came with the same change.
- [ADR-0021](0021-contain-module-faults-to-their-own-command.md): why module panics are recovered.
- [ADR-0025](0025-map-errors-to-connect-codes-only-in-rpc-handlers.md): which status codes the
  handler returns, and where they are mapped.
- Code: [cmds/dutagent/run.go](../../cmds/dutagent/run.go), and the status
  code mapping in [cmds/dutagent/errmap.go](../../cmds/dutagent/errmap.go).

Recorded retroactively in October 2026.
