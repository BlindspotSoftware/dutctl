# The TUI

For users of dutctl: the TUI (terminal user interface) that a bare `dutctl`
opens on a terminal, how to move in it, and what it remembers.

> **In short.** Run `dutctl` without arguments in a terminal and you get a
> prompt that walks from hosts to devices to commands. Type to filter, Enter to
> open, Esc to go up. A line you type is the command line you know, relative to
> where the prompt is: `power on` at a device runs what `dutctl <device> power
> on` runs. Finished work stays in your terminal's scrollback. Scripts never
> see the TUI.

## When it opens

A bare `dutctl` opens the TUI when both its input and its output are a
terminal, the output format is text, and `TERM` is set and not `dumb`.
Anywhere else, in a pipe, with `-f json`, in CI, it lists the devices as it
always did, and every form with arguments behaves exactly as before.

With `-s` the TUI starts at that host's devices; without, it starts at your
recent devices and hosts. The TUI connects only when you ask it to.

## Levels

The prompt shows where you are: `dutctl ›`, then `lab:2024 ›`, then
`lab:2024 › demo-board ›`. The menu under it lists what you can open there:

| Level   | The menu                                                        | Fetched on entry          |
|---------|-----------------------------------------------------------------|---------------------------|
| hosts   | Recent devices (with the last line run on each), Hosts          | nothing: the history file |
| host    | its devices, with their reservations and running commands       | `List`                    |
| device  | Commands with their first help line, then `lock` and `unlock`   | `Commands`, the help      |

Everything shown comes from the agent when you enter the level; the TUI keeps
no device or command lists of its own.

## Keys

| Key                       | Does                                                                 |
|---------------------------|----------------------------------------------------------------------|
| typing                    | filters the menu (case-insensitive, any part of the name)            |
| `enter`                   | opens the highlighted row, or runs the line                          |
| `tab`                     | puts the highlighted row into the line; on a device row, enters it   |
| `↑` `↓` `pgup` `pgdown`   | move the highlight; on a folded menu the first press unfolds it      |
| `esc`                     | clears the line, else goes one level up; it never quits              |
| `ctrl+r`                  | fetches the level again                                              |
| `ctrl+c`                  | clears the line; on an empty line, press it twice to quit            |
| `ctrl+d`                  | quits on an empty line                                               |
| `?`                       | on an empty line, shows the keys                                     |
| `ctrl+a` `ctrl+e` `ctrl+u` `ctrl+w` `ctrl+k` | edit the line                                     |

Enter on a command never runs it: it puts the command and a space into the
line and shows its help under it, and Enter on the line runs it. `lock` and
`unlock` are picked the same way, from the menu, with `tab`, or typed. A line
with arguments, `power on` or `lock 2h`, runs on the first Enter. The help
shows only while you type a command's arguments, up to 8 lines; `<command>
help` prints all of it.

After a run, and after `lock`, `unlock` and `<command> help`, the menu folds
to one line, so what the run printed stays in sight:

```
  › power · console · flash · protect · reboot · unprotect · update · lock · unlock
```

The highlighted command comes first. Typing or a move unfolds the menu; Enter
and `tab` act on the folded menu as on the full one.

## Lines

A line is split at spaces, with no quoting, and read from where the prompt is:

- at a device: `power on`, `lock 2h`, `unlock force`, `power help`;
- at a host: `demo-board power on` enters the device and runs `power on`;
- at the hosts: `lab:2024` connects; `demo-board power on` does all of it, when
  `demo-board` is a recent device. An address is dialled as you typed it, even
  when a known host contains it: `tab` or `↓` picks the known one.

Keys you type while the TUI waits for the agent are kept and act once its
answer is in, so a line typed ahead runs whole; `esc` drops them with the call.

The TUI does not check names: the agent does, and the TUI lands where you
can fix what it said.

## A run

A run takes the whole terminal, as the command line does, and leaves a block
in the scrollback:

```
● demo-board power on                              lab:2024 · ada@lab · 10:42:15
power: on
⎿ done · 0.3s
```

The output in between is what `dutctl <device> <command>` prints. A console
says when it opens and how to leave it, and when it closes, in its colour:

- `▌ line console` (blue): your terminal edits the line, Enter sends it,
  Ctrl-D ends your input and Ctrl-C ends the run. The TUI stays.
  `▌ line console closed` follows.
- `▌ raw console` (violet): every key goes to the device, Ctrl-C included.
  Ctrl-A x ends it, Ctrl-A twice sends Ctrl-A, Ctrl-A e toggles local echo.
  At the prompt Ctrl-A moves to the start of the line instead.
  `▌ raw console closed` follows.

Below the greeting, the TUI's own lines start with `●`, `⎿`, `▌`, `✗` or,
for warnings, `⚠`, or are indented under one, and are bold, coloured or gray;
text in your terminal's plain style is the module's. A TUI line always starts
a line of its own, also after output that did not end its line.

Among the recent devices at the start, one whose last line opened a raw
console carries a `⌨`. Keys you type during a run that no console takes are
dropped before the prompt returns, so an Enter typed during a long flash cannot
act on the menu.

## Where you land

After a run the prompt is on the same device, the highlight on the command you
ran, the menu folded, ready for the next one. A failure lands where it can be
fixed:

| The agent said                          | You land                                        |
|-----------------------------------------|-------------------------------------------------|
| wrong arguments                         | the device, your line kept, its help under it   |
| in use, reserved, already running       | the device, your line kept: Enter retries       |
| no such command                         | the device, its commands fetched again          |
| no such device                          | the host, its devices fetched again             |
| cannot reach the agent                  | the hosts, that host marked `unreachable`       |

A refusal comes with a hint, such as `unlock force` for another user's
reservation; see [device-access.md](./device-access.md).

## History

The TUI remembers the hosts and devices you entered and the lines the agent
accepted, in `$XDG_STATE_HOME/dutctl/history`, by default
`~/.local/state/dutctl/history`, readable by you only. It holds host
addresses, device names and the lines you typed, which may name local files;
nothing of it goes to the agent. A line the agent refused as unknown or wrong
is not kept. Delete the file to forget everything. Without a home directory the
TUI works without a history.

## Exit

`ctrl+d`, or `ctrl+c` twice, ends the TUI with exit code 0; SIGTERM and
SIGHUP end it with 130, after a run in progress returned. A failed run never
changes the exit code: the code tells how the session ended.

## Limits

- No live progress during a run: the TUI shows the run's output as it comes
  and the elapsed time at its end.
- Lock states are fetched when you enter a level and after each run, never on
  a timer: `ctrl+r` refreshes them.
- File paths in a line are relative to the directory you started dutctl in,
  shown in the footer.
- `-no-color` and `NO_COLOR` turn colour off; bold and the glyphs
  `› ● ⎿ ▌ ✗ ⌨` still carry the state.
