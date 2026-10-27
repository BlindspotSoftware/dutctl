---
status: superseded by ADR-0029
date: 2024-09-10
---

# Drive the Run handler with a generic state machine

## Context and problem statement

The first draft of dutagent, in August 2024, handled a Run request in one
function. It received the command, looked up the device and the command,
started the module and the two session workers on goroutines, and waited until
the module had finished.

Once these basic concepts worked, a refactoring set out to tidy up and harden
the agent's code, and it restructured this handler too. The pull request gave
no other reason for the state machine. The new package named the talk
"Lexical Scanning in Go" as its inspiration: each state is a function that
returns the next one.

## Decision outcome

The Run handler is driven by a generic finite state machine from a new
package, `internal/fsm`:

```go
type State[T any] func(ctx context.Context, args T) (T, State[T], error)
```

- `fsm.Run` calls one state after the other until a state returns no next
  state or an error. It checks the context before each state.
- The handler starts the machine with one argument value, `runCmdArgs`. It
  holds the dependencies, such as the stream and the broker, and the results
  of earlier states, such as the command message and the resolved command.
  Each state gets a copy and returns an updated one.
- There are four states, each with exactly one successor:
  `receiveCommandRPC`, `findDUTCmd`, `executeModules` and `waitModules`.
  `executeModules` starts the module on a goroutine and returns at once;
  `waitModules` waits for the module and the broker.

### Consequences

- Good, because each step of a run is a small function that maps its own
  failures to a status code and can be tested on its own.
- Good, because a new concern can become a new state.
- Bad, because cleanup cannot live in a state. The machine stops at the first
  error and skips the remaining states, so teardown has to sit in the
  handler.
- Bad, because the states pass their results on in one value that each state
  copies. Data that several states share, such as a lock the handler must
  release, needs a pointer or a channel.
- Bad, because starting the module and waiting for it are separate states, so
  the goroutine that runs the module is started in one state and awaited in
  another. In 2026 this split hid lifetime bugs.

## More information

- Superseded by [ADR-0029](0029-run-the-run-rpc-as-straight-line-code.md),
  which explains why the machine was removed.
- [ADR-0007](0007-route-module-input-and-output-through-a-session.md): the broker that the
  same refactoring introduced.
- Later changes:
  - 2025-12: the states moved into a file of their own and got unit tests.
  - 2026-06: per-device locking added three states, to check access, to take
    the device's auto-lock and to release it; the machine had seven states.
  - 2026-07: a deferred release in the handler, which also ran on a panic,
    replaced the release state; six states remained.

Recorded retroactively in October 2026.
