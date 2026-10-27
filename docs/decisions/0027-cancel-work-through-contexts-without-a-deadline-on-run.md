---
status: accepted
date: 2026-07-24
---

# Cancel work through contexts, without a deadline on Run

## Context and problem statement

Until late July 2026, cancellation reached few places:

- dutctl ran every RPC on a background context without a deadline. Ctrl-C
  killed the process in the middle of a call.
- A module's `Run` always got a context, and `Init` and `Deinit` got one
  earlier that month, but nothing bounded them. The shell, flash,
  flash-emulate and agent-status modules started their tools without the
  context, so cancelling a run did not stop the tool. The time-wait module
  slept through a cancellation.
- One HTTP client carries every RPC, the long-lived Run stream included.

How do client, agent and modules stop work that is no longer wanted, and which
waits get a time limit?

## Considered options

- **Timeouts on the HTTP client**: bound each call with `http.Client.Timeout`
  or the transport's `ResponseHeaderTimeout`.
- **Cancellation through contexts**: pass a context through every layer, give
  only short calls a deadline, and have modules honor their context.

## Decision outcome

Chosen option: **cancellation through contexts**, because the HTTP client is
shared with Run. `Client.Timeout` bounds the whole exchange, response body
included, and the agent sends Run's response headers only with its first
message. Either timeout would cut a long run, or one that is slow to print.

- Work stops when its context is done. Every layer passes its context on, and
  a blocking call takes the context or selects on it.
- Modules honor their context, as the contract in
  [pkg/module/module.go](../../pkg/module/module.go) requires: `Init`, `Run`
  and `Deinit` return promptly once it is done. `Run`'s context ends when the
  command is aborted or the client disconnects, `Init`'s 5 minutes after the
  modules began to initialize, and `Deinit`'s 15 seconds after
  deinitialization began.
- Modules that run external tools start them through `internal/procexec`, in
  a process group of their own, and cancellation signals the whole group.
  `exec.CommandContext` alone signals only the direct child: a forked child
  survives and, holding the output pipes, keeps `Wait` blocked. Most tools
  get `SIGKILL`; flash gets `SIGTERM` and 10 seconds to stop, so a write is
  not cut midway.
- dutctl uses one context per invocation, cancelled by `SIGINT` or `SIGTERM`,
  and an RPC that a signal ends exits with status 130. Each unary RPC adds a
  30-second deadline, and the agent's registration with a dutserver one of 10
  seconds. The connect library sends the deadline along as a `grpc-timeout`
  header, so the handler on the other side has the same one.
- Run gets no deadline. A command takes as long as it takes, and some, such as
  the serial module's monitor mode, run until the user stops them. The HTTP
  transport bounds only dialing (10 seconds) and idle pooled connections (90
  seconds), and the agent's server only the reading of request headers.

### Consequences

- Good, because Ctrl-C ends any RPC through the normal teardown, with the
  same exit status for a unary call as for Run.
- Good, because cancelling a run also stops the tools it started and their
  children.
- Good, because an unreachable agent fails the dial after 10 seconds, also for
  Run, and an unresponsive one fails a unary call after 30 seconds.
- Bad, because cancellation reaches only as far as each module honors it. A
  call that ignores the context, such as `time.Sleep`, delays the stop; what
  that means for the device is decided in
  [ADR-0031](0031-keep-a-device-busy-until-its-modules-return.md).
- Bad, because a run whose connection died silently ends only once the agent
  notices the loss, which can take minutes.

### Confirmation

- Tests in [cmds/dutctl](../../cmds/dutctl) check that every unary RPC
  carries a deadline and the caller's cancellation, and that a signal maps to
  the interrupted exit.
- A test in [internal/procexec](../../internal/procexec) checks that
  cancellation also kills a forked child.
- A test in [internal/rpc](../../internal/rpc) checks that the server lifts
  its header timeout once a connection is active, so it cannot cut a stream.
- The `noctx` and `contextcheck` linters in [.golangci.yml](../../.golangci.yml)
  flag calls made without a context, or with one not derived from the
  caller's.

## More information

- [ADR-0007](0007-route-module-input-and-output-through-a-session.md): how a module waiting
  in a session call is released when its run ends.
- [ADR-0009](0009-initialize-every-module-once-at-agent-start.md): when `Init` and
  `Deinit` run.
- [ADR-0021](0021-contain-module-faults-to-their-own-command.md): how a module that fails or
  panics is contained.
- [ADR-0031](0031-keep-a-device-busy-until-its-modules-return.md): what happens
  to the device while a cancelled module has not returned.
- [ADR-0032](0032-stop-the-agent-in-stages.md): how stop signals reach
  running commands.
- [docs/dutagent-shutdown.md](../dutagent-shutdown.md): which signal a tool
  gets and when, and how long a lost connection takes to notice.

Recorded retroactively in October 2026.
