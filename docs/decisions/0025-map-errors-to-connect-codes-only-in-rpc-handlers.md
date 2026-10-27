---
status: accepted
date: 2026-07-24
---

# Map errors to connect codes only in RPC handlers

## Context and problem statement

Errors cross two kinds of boundaries in dutctl: between Go packages, and
between agent and client, where connect carries a status code and a message.
In 2024 the maintainers asked packages to prefer their own error types over
passing third-party errors through, such as those of the validator library:
returned errors, wrapped ones included, become part of a package's API, so
replacing the dependency later may break callers. For the same reason the
wrapcheck linter, which demands that errors from other packages be wrapped,
stayed off.

By mid-2026 the codes the agent sent were inconsistent. An unknown device or
command came back as `InvalidArgument`, and a cancelled run could reach the
client as `Aborted` or `Internal`. A malformed file transfer from the client
had no sentinel to map it by, and it was logged three times on its way up.

## Decision outcome

Errors stay plain Go values inside the code base and become connect codes only
at the RPC boundary.

- A package returns its own sentinels and error types, wrapped with context,
  and its doc comments say which of them callers may match with `errors.Is` or
  `errors.As`. Third-party error types are not part of that contract:
  `pkg/dut`, for example, turns validator errors into its own
  `ErrModuleValidation`.
- Only the RPC layer assigns connect codes: the handlers of dutagent and
  dutserver, and the version interceptor in `internal/rpc` in front of the
  agent's handlers. Each dutagent handler that can fail lists its codes in an
  `Errors:` paragraph of its doc comment.
- The agent maps errors as follows:
  - `NotFound`: the device or command does not exist.
  - `InvalidArgument`: the client must change the request, for example an
    unknown keyword, arguments that do not resolve, a negative lock duration
    or a malformed file transfer.
  - `FailedPrecondition`: the request is valid but cannot be served now, for
    example a device held by another owner, nothing to unlock, or a client of
    another major version.
  - `PermissionDenied`: releasing a lock that another owner holds.
  - `Unauthenticated`: locking, or a release without force, by a caller
    without a name.
  - `Aborted`: a module failed, or the run broke off before its command came.
    A module's error reaches the client as text; the agent checks it only for
    a cancellation.
  - `Canceled` or `DeadlineExceeded`: the run was cancelled, with the same
    code wherever the cancellation is noticed.
  - `Internal`: anything else; a connect error from the transport keeps its
    code.
- An error is logged once, where it becomes terminal; on the agent that is the
  RPC handler. The layers in between return it without logging. A lower layer
  may log one detail line in its own scope when the summary would lose what it
  knows, such as which module failed, and a comment marks it as deliberate.

### Consequences

- Good, because clients and scripts can act on the code: an unknown device, a
  busy device and a failed module each get their own.
- Good, because the domain packages stay free of connect and can be tested and
  reused without it.
- Good, because a dependency can be replaced without changing what callers
  match.
- Bad, because the contracts are prose in doc comments: a later change can
  leave them stale, and no tool compares them with the code.
- Bad, because a module's error loses its type at the boundary: the client sees
  `Aborted` and the message.

### Confirmation

The tests in [cmds/dutagent](../../cmds/dutagent), `errmap_test.go`,
`rpc_test.go` and `run_test.go`, check the codes that the handlers and the
mapping helpers return.

## More information

- [ADR-0021](0021-contain-module-faults-to-their-own-command.md): what happens when a module panics
  instead of returning an error.
- [ADR-0022](0022-log-through-a-scoped-logger-in-the-context.md): how the agent logs,
  and where an error is logged.
- [ADR-0023](0023-reject-rpcs-from-another-major-version.md): the version check that
  answers with `FailedPrecondition`.
- [cmds/dutagent](../../cmds/dutagent): the handlers in `rpc.go` and `run.go`
  with their `Errors:` paragraphs, and the mapping helpers in `errmap.go`.
- Later changes:
  - 2026-10: the Run mapping moved to `cmds/dutagent/errmap.go` when the Run
    handler became straight-line code. `FailedPrecondition` was extended to a
    device that is already running a command, a forced unlock of a busy device
    and an agent that is shutting down, and `Aborted` to a run that the agent
    aborts while it stops (see
    [ADR-0029](0029-run-the-run-rpc-as-straight-line-code.md) to
    [ADR-0032](0032-stop-the-agent-in-stages.md)).

Recorded retroactively in October 2026.
