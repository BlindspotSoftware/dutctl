---
status: accepted
date: 2024-09-10
---

# Route module input and output through a session

## Context and problem statement

A module reaches its user only through the `module.Session` it gets in `Run`
([ADR-0004](0004-compile-modules-into-the-agent-behind-a-session.md)), and the agent talks to
the client over one bidirectional Run stream
([ADR-0003](0003-run-a-command-over-one-bidirectional-stream.md)). Some part of the agent has
to turn the module's calls into stream messages, and the client's messages
into input the module reads. Both directions are active at the same time: a
module may print while the client sends input.

The first draft of the agent did this inside the Run handler, with a session
of Go channels and one goroutine per direction. The refactoring of September
2024 moved this job into a component of its own.

## Decision outcome

For every run, the agent creates a broker that sits between the module and the
stream:

- The broker builds the `module.Session` from unbuffered Go channels, one per
  kind of message: print, stdin, stdout, stderr, file request and file.
  `internal/chanio` adapts the console channels to `io.Reader` and
  `io.Writer`.
- Two workers translate, one per direction. One turns what the module sends
  into `RunResponse` messages; the other turns `RunRequest` messages into
  input for the module.
- The broker reaches the stream only through a `Stream` interface with `Send`
  and `Receive`. Modules never see the stream, the protobuf messages or
  connect.
- A failure to deliver output to the client, such as a broken stream, is not
  reported to the module. The broker fails the run instead.

### Consequences

- Good, because modules do not depend on the transport: no module imports the
  protobuf or connect packages.
- Good, because a module's write returns only once a worker has taken it, so
  a slow client slows the module down instead of piling up output in the
  agent.
- Good, because one worker sends all of a module's messages, in the order the
  module wrote them.
- Good, because the broker can be tested with a fake `Stream` instead of a
  network connection.
- Bad, because a session call blocks until a worker takes part. When the
  workers end first, for example after the client disconnected, the call
  needs another way out (see Later changes).
- Bad, because the worker that carries the client's messages waits until the
  module has read its input. Input that no module reads holds up the client's
  later messages, a file upload among them.
- Neutral, because a print returns once a worker has taken the message, not
  once it is on the wire. The handler has to wait for the workers before it
  returns ([ADR-0031](0031-keep-a-device-busy-until-its-modules-return.md)).

### Confirmation

The tests in [internal/dutagent/session](../../internal/dutagent/session)
drive the broker and the session through a fake stream, and the tests in
[internal/chanio](../../internal/chanio) cover the channel adapters.

## More information

- [ADR-0006](0006-drive-the-run-handler-with-a-generic-state-machine.md): the state machine that
  the same refactoring introduced in the Run handler.
- Code: [internal/dutagent/session](../../internal/dutagent/session),
  [internal/chanio](../../internal/chanio), and the `Session` contract in
  [pkg/module/module.go](../../pkg/module/module.go).
- [ADR-0035](0035-frame-consoles-in-line-or-raw-mode.md): consoles framed by
  messages of their own, each with channels of its own; `OpenConsole`
  replaces `Console()`.
- Later changes:
  - 2025-12: `Start` returned the session together with an error channel
    that closed once both workers had ended, and a worker that ended stopped
    the other one.
  - 2026-07: a done signal frozen onto the session unblocked a module stuck
    in a session call once the workers had ended. Prints were dropped, file
    transfers failed, stdin reported end of file and console writes failed.
  - 2026-10: `Start` returned the context to run the modules under, which
    the first worker failure cancelled, and `Stop` stopped the workers and
    waited for them.

Recorded retroactively in October 2026.
