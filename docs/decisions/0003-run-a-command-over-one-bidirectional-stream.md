---
status: accepted
date: 2024-08-13
---

# Run a command over one bidirectional stream

## Context and problem statement

Finding out what an agent offers takes simple request and response calls: list
the devices, list the commands of a device. Running a command is different. It
can run for a long time, print its progress, talk with the user through a
console, and move files such as firmware images or dumps between the user's
machine and the agent.

The question was how a client starts a command, and how both sides exchange
these messages while it runs.

## Decision outcome

Discovery uses unary RPCs; running a command uses one bidirectional stream.

- `List` and `Commands`, and since September 2024 `Details`, are unary calls.
- `Run` is a bidirectional stream that the client opens for one command. Its
  first `RunRequest` carries a `Command`: the device, the command and its
  arguments. The agent rejects a stream that starts with anything else.
- After that, the stream carries the command's `Print` output, console bytes
  in both directions, and file transfers.
- A file transfer is always started by the module on the agent. It either
  requests a file from the client (`FileRequest`, answered by one `File`) or
  sends one (`File`).
- A file moves whole, in one message, and only one transfer is in flight at a
  time. The agent fails the run on a file it did not request.
- The agent ends the stream by returning from its handler; the handler's error,
  if any, is the status of the RPC. There is no end-of-run message.

### Consequences

- Good, because everything that belongs to one command travels on one call, so
  nothing has to be matched up across connections.
- Good, because the client stays generic: it prints output, forwards input and
  answers file requests without knowing the command.
- Bad, because a file is held in memory in full on both sides, and other
  messages on the stream wait while it is in flight.

### Confirmation

Tests in [cmds/dutagent/run_test.go](../../cmds/dutagent/run_test.go) check
that a stream that does not start with a `Command`, and a file that the agent
did not request, both fail with `InvalidArgument`.

## More information

- [docs/README.md](../README.md#communication-design): the message scenarios on
  the Run stream.
- [protobuf/dutctl/v1/dutctl.proto](../../protobuf/dutctl/v1/dutctl.proto):
  `RunRequest` and `RunResponse`.
- [ADR-0002](0002-speak-grpc-over-cleartext-http-2-with-connect-go.md): the RPC layer under the stream.
- [ADR-0007](0007-route-module-input-and-output-through-a-session.md): how a module's I/O
  reaches the stream.
- [ADR-0035](0035-frame-consoles-in-line-or-raw-mode.md): the messages that
  frame a console on the stream, added in 2026.
- [ADR-0014](0014-let-the-agent-access-client-files-the-user-did-not-name.md): which paths
  on the client a file transfer may use.
- Later changes:
  - 2024-09: file transfer landed; the agent fails a run on a file it did not
    request.
  - 2026-03: the end of the client's input no longer ended a run; the client
    waited until the agent closed the stream.
  - 2026-06: the unary calls `Lock` and `Unlock` joined for device locks
    ([ADR-0017](0017-lock-devices-in-agent-memory.md)).

Recorded retroactively in October 2026.
