---
applyTo: "internal/dutagent/**,internal/chanio/**,cmds/dutagent/**,internal/rpc/stream.go,internal/rpc/server.go,pkg/module/module.go"
---

# Review rules for the agent's core

These paths hold the goroutines of a run: the Run handler, the session broker and its two workers,
the consoles, the channel adapters in `internal/chanio`, the locker, and the stop stages. A
mistake here does not fail one command: it blocks a run forever, keeps a device busy, races, or
ends the agent with every run on it. Review every new goroutine, channel operation, lock and timer
here with care: what ends it, and what happens to it when the run is torn down. The decisions in
force here are ADR-0007, ADR-0017, ADR-0021, ADR-0025, ADR-0027, ADR-0029, ADR-0031, ADR-0032,
ADR-0035 and ADR-0036; GEN-1, GEN-2 and GEN-3 say when a change needs a new or superseding ADR or
is breaking.

## Session and channels

- [CORE-1] Every module-facing session call that waits on a worker selects on the done signal the
  broker froze onto the session (`s.done`), or hands its chanio adapter a signal that ends with
  it, as `OpenConsole` does with the console's `eof` and `closed`; a bare channel send or receive
  there blocks the module forever once the workers are gone. A worker's send to the module
  goroutine selects on `ctx.Done()`.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0007-route-module-input-and-output-through-a-session.md#more-information
- [CORE-2] The session's channels are unbuffered, so a slow client slows the module down and
  output keeps its order. The only buffered one among them is a file's channel of capacity 1,
  which its sender fills and closes at once. Once the broker runs, only `toClientWorker` sends on
  the stream.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0007-route-module-input-and-output-through-a-session.md#decision-outcome
- [CORE-3] `Broker.Start` sets the fields that the module-facing methods read without a lock (the
  channels, `done`, `log`) before it starts the workers, and nothing reassigns them later. The
  fields that `backend.mu` guards, `currentFile`, `currentConsole` and `lastConsoleID`, are read
  and written only under it.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/internal/dutagent/session/session.go
- [CORE-4] A module has one console at a time: a second `OpenConsole` ends the first. Input or
  control that is not marked for the open console is dropped, never kept for a later one. A
  console ends through its signal channels, each closed once through its `sync.Once`; its data
  channels are never closed, because a `ChanWriter` panics on a closed channel.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0035-frame-consoles-in-line-or-raw-mode.md#decision-outcome
- [CORE-5] `OpenConsole` hands the console's open event to `toClientWorker` before it returns the
  console, and the close event follows only once the console's channels have ended, so the client
  sees the open before the first output and the close after the last. The module runner calls
  `Broker.CloseConsole` from the goroutine that ran the module, once the module returned, so no
  console outlives its module.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/internal/dutagent/session/broker.go

## Run handler and modules

- [CORE-6] `run` stays straight-line code: it receives the command, finds it and resolves its
  arguments, and only then takes the device with `AutoLock`. Each release is deferred right after
  its acquisition. Access check and hold are one call under the locker's mutex; no separate
  check-then-lock step, no state machine.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0029-run-the-run-rpc-as-straight-line-code.md#decision-outcome
- [CORE-7] Modules run on the Run handler's goroutine through `catchPanic`. The handler returns,
  and the run's lease ends, only after the modules and both workers have stopped; no stream
  `Send` may outlive the handler. No timer cuts that wait short: while a cancelled module has not
  returned, the agent warns every 10 seconds instead.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0031-keep-a-device-busy-until-its-modules-return.md#decision-outcome
- [CORE-8] A module's panic is recovered at the module boundary, by `catchPanic` around `Init`,
  `Run` and `Deinit`, and fails only that call. Besides it, only `start` recovers a panic of the
  main flow, which aborts the running commands as stage 2 does before the agent exits. A goroutine
  the agent starts, such as a broker worker, has no recover and must never panic: the panic would
  end the agent with every run on it.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0021-contain-module-faults-to-their-own-command.md#decision-outcome

## Locks and stopping

- [CORE-9] Each run holds its device through a lease of its own, and only that run releases it.
  `unlock force`, an expiring reservation and the abort stage leave leases alone. The same command,
  by device and command name whatever its arguments, is refused while it runs or stops; a user's
  other commands are admitted. Locks live in agent memory only.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0036-run-a-users-different-commands-side-by-side.md#decision-outcome
- [CORE-10] Stop stages escalate by stop signals, and a failure of the agent itself enters the
  abort stage directly; no stage has a deadline. Each stage puts the locker into its mode before it
  ends its stage context. `Deinit` runs only once no module runs, for at most 15 seconds
  (`deinitTimeout`), through `deinitWithin`, which leaves behind a module that ignores its context.
  Work refused while stopping gets FailedPrecondition, never Unavailable, and a run aborted by the
  stop gets Aborted, wrapping `errAbortedByShutdown`.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0032-stop-the-agent-in-stages.md#decision-outcome

## Error codes

- [CORE-11] Where the agent maps a cancellation itself, it uses `cancelCode` in `errmap.go`, so a
  cancelled run gets the same code wherever it is noticed. A connect error that already carries a
  code keeps it, and a run the stop aborted gets Aborted (CORE-10). `session`, `locker` and
  `chanio` never import connect (GEN-8). The session passes the stream's errors on, so
  `brokerError` keeps the transport's code.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0025-map-errors-to-connect-codes-only-in-rpc-handlers.md#decision-outcome
- [CORE-12] A dutagent handler picks the code by what the caller can do about the failure:
  NotFound for an unknown device or command; InvalidArgument when the client must change the
  request, such as an unknown keyword, arguments that do not resolve, a negative lock duration or a
  malformed file transfer; FailedPrecondition when a valid request cannot be served now, such as a
  device another owner holds or that already runs the command, nothing to unlock, a forced unlock
  of a busy device, a client of another major version or a stopping agent; PermissionDenied for
  releasing another owner's lock; Unauthenticated for locking, or a release without force, by a
  caller without a name; Aborted for a failed module, a run that broke off before its command
  came, or a run aborted by the stop; Internal for anything else, while a connect error from the
  transport keeps its code.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0025-map-errors-to-connect-codes-only-in-rpc-handlers.md#decision-outcome

## Tests

- [CORE-13] A change of behavior in `internal/dutagent/session` comes with a unit test there that
  drives the broker and the session through a fake stream; one in `internal/chanio` with a test of
  the channel adapters.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0007-route-module-input-and-output-through-a-session.md#confirmation
- [CORE-14] A change of behavior on the Run path is tested in `cmds/dutagent/run_test.go`, which
  drives `run` through a fake client stream and checks what reaches the client, the status code,
  and that the device is busy while its modules run and free afterwards.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0029-run-the-run-rpc-as-straight-line-code.md#confirmation
- [CORE-15] A new or changed `module.Session` feature gets a use in a demo module in
  `pkg/module/demo`, a `TestSession` test in `test/integration` and a row in the table of
  `pkg/module/demo/README.md`.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/pkg/module/demo/README.md#what-the-modules-show

## Do not flag

- A file's channel of capacity 1 in the session: it is the session's one deliberate buffered
  channel (CORE-2). CORE-2 covers only the session's channels, not others such as the signal
  channel in `shutdown.go` or a result channel that a goroutine sends to once.
- A detail line that `runModules` or the broker logs in its own scope with a comment marking it as
  deliberate (GEN-9).
- A goroutine left running on purpose whose comment says why, such as the broker's receive
  goroutine or a `Deinit` that outlives `deinitWithin`.
