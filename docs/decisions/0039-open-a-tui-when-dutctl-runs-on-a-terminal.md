---
status: proposed
date: 2026-11-14
---

# Open a TUI when dutctl runs on a terminal

## Context and problem statement

dutctl is a one-shot command line: each call names a device and a command
([ADR-0010](0010-use-a-positional-cli-grammar-with-reserved-keywords.md)),
makes one RPC and exits
([ADR-0001](0001-split-dut-control-into-an-agent-and-a-thin-client.md)). In
daily interactive use, the beta phase of
[ADR-0028](0028-tier-the-public-interfaces-for-v1-0-0.md), a firmware developer
types the server, the device and the command again for every step, and runs
`list` and `help` to find the names. Scripts and CI use the same binary and
rely on a bare `dutctl` listing the devices.

How can dutctl let a person find and run commands interactively without
changing anything a script sees, and how does it run a command from there?

## Decision drivers

- Pipes, structured output formats and every call with arguments behave as
  before.
- The agent stays the only source of devices, commands and help.
- A run from the TUI (terminal user interface) behaves like the same run on
  the command line, consoles and their escape key included
  ([ADR-0035](0035-frame-consoles-in-line-or-raw-mode.md)).
- No second grammar: a line typed into the TUI is a command line.

## Considered options

- **A run in the same process**: the TUI hands the terminal to the command
  line's run path and takes it back when the run returns.
- **A child dutctl per run**: the TUI starts `dutctl <device> <command>` and
  waits for it.
- **Captured output**: the TUI keeps drawing during a run and prints the
  run's output itself, until a console opens and it hands over the terminal.

## Decision outcome

Chosen option: **a run in the same process**, because only it tells the TUI
which console opened and how the run ended, as a Connect code, without drawing
during the run. A child process reports an exit code only and decides its
identity on its own. Captured output hands over the terminal in the middle of a
stream, where the TUI's last lines and the console's first bytes could race.

- A bare `dutctl` opens the TUI when stdin and stdout are terminals, the
  output format is text, and `TERM` is set and not `dumb`. Otherwise it lists
  the devices as before, and every call with arguments is unchanged. This adds
  to ADR-0010 and leaves its grammar and keywords as they are: a line in the
  TUI is that grammar, read from the host or device the prompt is on.
- The TUI is inline: a prompt, a menu under it and a footer, while finished
  work stays in the terminal's scrollback. A run uses the command line's code
  in the same process and has the terminal until it returns. The run path
  stops reading stdin when a run ends, so a later run in the same process gets
  every key; the command line shares this code.
- The TUI keeps a history of the hosts and devices entered and of the lines
  the agent accepted, in `$XDG_STATE_HOME/dutctl/history`, readable by the user
  only. The history orders menus and fills lines. Every level is fetched from
  the agent before anything runs, and no device list, command list or help text
  is stored. This adds to ADR-0001: the client keeps state of its own, but no
  knowledge of the devices.
- The TUI owns the terminal. The rule of
  [ADR-0019](0019-keep-command-output-on-stdout-and-diagnostics-on-stderr.md)
  applies to command-line calls and to a run's output inside the TUI. The
  TUI draws on stdout and prints diagnostics above the prompt, or straight to
  the terminal during a run. Warnings are summarized after each run and at
  quit.
- Ctrl-C ends a run, never the TUI. The exit code tells how the session
  ended: 0 on quit, 130 when a signal ended it, 1 when the TUI cannot run. A
  failed run does not change it.
- While a run holds the terminal, nothing the run calls goes through Bubble
  Tea's `Program.Send` or `Println`, the terminal UI library's calls that hand
  a message or a line to its event loop: the event loop waits for the run, so
  the call would deadlock. The run writes its lines, diagnostics included,
  through the console's writers. A message from another goroutine waits until
  the run has returned.

### Consequences

- Good, because a developer moves from host to device to command, sees the
  help beside the line and returns to a recent device from the start.
- Good, because scripts, pipes and `-f json` see no change.
- Good, because a run from the TUI is a command-line run, with the same
  output, consoles, escape key and errors.
- Bad, because nothing moves on the screen during a run without output: the
  elapsed time shows only when the run has ended.
- Bad, because a panic in the run's send or receive routine is not recovered,
  as on the command line: it can leave the terminal raw until the user runs
  `reset`.
- Bad, because the history file keeps host addresses, device names and typed
  arguments on the user's machine, until the user deletes it.
- Neutral, because the TUI belongs to the command line, the best-effort tier
  of ADR-0028: its keys and its look may change between releases.

### Confirmation

`TestOpensTUI` and `TestDispatch` in
[cmds/dutctl/dutctl_test.go](../../cmds/dutctl/dutctl_test.go) pin the gate.
`TestTUI` in
[test/integration/tui_test.go](../../test/integration/tui_test.go)
runs the TUI on a terminal and checks that a bare `dutctl` on a pipe, or with
`-f json`, prints what `dutctl list` prints.

## More information

- [ADR-0010](0010-use-a-positional-cli-grammar-with-reserved-keywords.md),
  [ADR-0001](0001-split-dut-control-into-an-agent-and-a-thin-client.md) and
  [ADR-0019](0019-keep-command-output-on-stdout-and-diagnostics-on-stderr.md):
  the decisions this record adds to.
- [ADR-0035](0035-frame-consoles-in-line-or-raw-mode.md): the consoles a run
  opens, and their escape key.
- [docs/dutctl-tui.md](../dutctl-tui.md) describes the TUI
  for users; [cmds/dutctl/tui.go](../../cmds/dutctl/tui.go) and
  [cmds/dutctl/tui_run.go](../../cmds/dutctl/tui_run.go) implement it.
