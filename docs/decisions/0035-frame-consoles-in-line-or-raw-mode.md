---
status: accepted
date: 2026-10-05
---

# Frame consoles in line or raw mode

## Context and problem statement

Since 2024, one `Console` message carried stdin, stdout and stderr on the Run
stream ([ADR-0003](0003-run-a-command-over-one-bidirectional-stream.md)), and modules got the
three streams from `Session.Console()`. Nothing told the client when a console
began or ended, or how the module meant to use it.

So dutctl forwarded its input line by line from the start of every run and
never passed on its end. No module could bridge a DUT's serial port: input came
in whole lines only, and Ctrl-C ended the run instead of reaching the device.
On the agent, input that no module read held up everything the client sent
after it, a requested file included.

Users who wanted to type at a DUT's serial console ran a terminal program on
the agent host. The maintainers wanted dutctl to offer such a console itself,
as a basis for later interactive modules too, while piped input, CI jobs and
structured output keep working. How does a module open a console and say how
it uses it, and who sets up the user's terminal?

## Considered options

- **Console messages with a mode**: messages of their own frame each console,
  name its mode, LINE or RAW, and carry the input and its end.
- **An in-band marker**: a marker in the module's first console output opens
  the console; the protocol and the Session stay as they are.
- **Raw consoles only**: every console is a byte stream, with no mode.
- **Terminal flags**: the module sets echo, line editing and the like.
- **A compatibility path**: as the first option, but `Console()` stays, and
  console output without an open message counts as a line console.

## Decision outcome

Chosen option: **console messages with a mode**, because only messages of
their own tell the client reliably when a console begins and ends and how to
set up the terminal. Two modes name the two ways a client has to serve a
console anyway, since a pipe cannot be made raw.

A marker could come from the device's own output, and a client that held input
back until it saw one would starve a module that reads before it writes. Raw
consoles only would still serve piped input cooked, as an unnamed second mode,
and leave every prompt to echo and edit lines itself. Flags would put terminal
mechanics into a protocol meant to stay compatible from v1.0.0
([ADR-0028](0028-tier-the-public-interfaces-for-v1-0-0.md)). While DUT Control is in
alpha, the maintainers preferred a clean break to a compatibility path.

- The agent opens a console with `ConsoleOpen`, which carries the mode and an
  id unique within the run, and ends it with `ConsoleClose`. `ConsoleOutput`
  carries the module's stdout and stderr, `ConsoleInput` the user's input,
  marked with the id. `ConsoleControl` with an eof event ends that console's
  input only; output and file transfers go on.
- The client forwards input only while a console is open, and the agent drops
  input not marked for the open console. No hop alters the bytes. Print may be
  used while a console is open.
- A module opens its console with `OpenConsole`, which replaces `Console()`,
  and names the mode: LINE, the zero value, or RAW. Each console has channels
  of its own, and the agent closes it when the module's `Run` returns.
- Only dutctl touches the terminal. It switches to raw mode for a RAW console
  only when stdin is a terminal and the output format is text, and otherwise
  serves it like a LINE console. So a module must rely neither on raw mode nor
  on how its input is chunked. While raw, the module's bytes reach the terminal
  unchanged, and the client's own text gets CR LF line ends. Raw mode ends with
  the console, and with the run however it ends.
- In raw mode, Ctrl-C goes to the device. A fixed escape key takes its place,
  as in screen and picocom: Ctrl-A x ends the session, Ctrl-A twice sends one
  Ctrl-A, and Ctrl-A e toggles local echo. On Ctrl-A x the client tells the
  module that its input ended, and cancels the run if it still runs 3 seconds
  later. A configurable escape key was left out to keep it simple.
- What the bytes mean to a device is up to its module. For example,
  [`serial -i`](../../pkg/module/serial/README.md#interactive-mode) bridges the
  port verbatim, and the serial module's `imap` option maps the line endings of
  a device that ends its lines with a bare LF.
- There is no compatibility path: dutctl and dutagent are updated together.
  The field number of the old `Console` message stays reserved, so a client
  of an older alpha does not take console output for a file request. The
  `dummy-console` module replaces `dummy-repeat`.

### Consequences

- Good, because the client sets up the terminal from what the module states,
  and keys and output pass unchanged: full-screen programs on a DUT work
  through `serial -i`.
- Good, because the agent never queues input marked for no open console, so
  such input never reaches a later console or holds up a file transfer.
- Bad, because the RPC protocol and the module API break, configurations that
  name `dummy-repeat` no longer load, and mixed versions of dutctl and dutagent
  are not supported.
- Bad, because Ctrl-C does not end a raw console session, and the device gets a
  Ctrl-A only when it is typed twice.
- Bad, because a client killed with `SIGKILL` cannot restore the terminal.
- Bad, because a module with an open console must keep reading its input or
  close it: until then, everything the client sends after that input waits.

### Confirmation

Tests in `internal/dutagent/session/console_test.go` cover the framing and the
routing of input, and `TestRunFramesConsoles` in `cmds/dutagent/run_test.go`
checks that a console closes when its module returns. In `cmds/dutctl`,
`console_linux_test.go` checks raw mode on a pseudo-terminal and
`escape_test.go` the escape key. The FirmwareCI serial test drives `serial -i`
through pipes and checks the bytes in both directions.

## More information

- [docs/README.md](../README.md#communication-design) describes the console
  protocol, and [docs/module_guide.md](../module_guide.md#console) the module
  contract. [cmds/dutctl/console.go](../../cmds/dutctl/console.go) and
  [session/console.go](../../internal/dutagent/session/console.go) hold the
  two ends, and [pkg/module/dummy](../../pkg/module/dummy) the `dummy-console`
  example.
- [ADR-0003](0003-run-a-command-over-one-bidirectional-stream.md),
  [ADR-0004](0004-compile-modules-into-the-agent-behind-a-session.md) and
  [ADR-0007](0007-route-module-input-and-output-through-a-session.md): the Run stream, and
  the Session and broker through which modules reach the user.
- [ADR-0019](0019-keep-command-output-on-stdout-and-diagnostics-on-stderr.md): stdout for command
  output; the escape key hint goes to stderr.
- [ADR-0028](0028-tier-the-public-interfaces-for-v1-0-0.md): the RPC protocol becomes
  stable with v1.0.0, after this change.
- [ADR-0030](0030-run-one-command-at-a-time-per-device.md): an open console keeps the
  device busy for every other command.
