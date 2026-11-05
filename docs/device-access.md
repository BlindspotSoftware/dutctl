# Device access

For users of dutctl: who can use a device when, what dutagent takes care of, and
what is up to you when you run several of your commands on one device at once.

> **In short.** A device belongs to one user at a time: from `lock` until
> `unlock` or the reservation's end, and while any of their commands runs, until
> its modules have returned. That user may run several of their commands on the
> device at once, but never the same command twice. dutagent keeps other users
> out and keeps its own bookkeeping straight. What your commands do to the device
> together is up to you, and hardware that several commands share is up to your
> admin.

## Terms

- **You**: the name dutctl gives the agent, `<user>@<host>` by default or what
  you pass with `-u`.
- **Command**: the name after the device, as the agent's configuration defines
  it; `dutctl <device>` lists them. Its arguments do not count:
  `flash read a.bin` and `flash write b.bin` are one command, `flash`.
- **Running**: a command runs from its start until its modules have returned,
  also after Ctrl-C or a lost connection. A flash tool takes up to 10 s to stop.
- **Reservation**: a hold you take with `dutctl <device> lock [duration]`.

## Who you are

dutagent takes the name dutctl sends as given and checks nothing. Holds keep
cooperating users out of each other's way; they keep nobody out. All your
terminals and scripts on one machine are one user, and they may run commands on
the same device at once. Give scripts and CI jobs a name of their own with
`-u`, such as `-u "ci-${CI_JOB_ID:?}@runner"`, so they stay apart from each
other and from you. Where `$USER` or the host name is not set, as in some CI
containers, always pass `-u`.

## Reservations

`dutctl <device> lock [duration]` reserves a device for you, for 30 minutes
unless you give a duration; `lock` again extends it. While it lasts, nobody else
can start a command on the device or reserve it. `dutctl <device> unlock` ends
it. `dutctl <device> unlock force` ends anyone's reservation, for one its owner
forgot, but never a running command. Reserve a device for a session of several
commands: between two of your commands, a device you have not reserved is free
for anyone.

## Who can start what

| The device … | `dutctl dev cmd` |
| --- | --- |
| runs nothing | starts |
| runs other commands of yours | starts next to them |
| runs `cmd` for you, or is still stopping it after a cancel | refused |
| is reserved by someone else, or runs their command | refused |
| belongs to a dutagent that is stopping | starts only on a device you reserved |

The refusals read:

```
device "dev" is already running "flash" for "alice@lab"; a command does not run twice at once, and a cancelled one keeps running until it has stopped
device "dev" is in use by "bob@lab"
device "dev" is locked by "bob@lab" for 25m
dutagent is shutting down and takes no new work; try again once it is back
```

## Running your commands side by side

To watch a device boot while you switch its power, with `console` configured as
`serial -i`:

```
terminal 1$ dutctl dev console              # Ctrl-A x ends it
terminal 2$ dutctl dev power off
terminal 2$ dutctl dev power on
```

- Wait for `--- Connected to … ---` before you switch the power, or the first
  lines of the boot are lost.
- You need no reservation for it, but one keeps others out between your
  commands.
- `dutctl list` shows the device in use by you until the last of your commands
  has returned.

## What dutagent takes care of

- No other user gets a device while you reserve it or run a command on it.
- Each of your commands holds the device on its own: the device stays yours
  until the last of them has returned, in whatever order they end.
- A command never runs twice at once on a device, also not with other
  arguments. A cancelled command counts until its modules have stopped.
- The end of a reservation never ends a command: not when it expires, not on
  `unlock`, not on `unlock force`.
- dutagent stops only once every command has returned, unless its admin sends a
  third stop signal or kills it.

## What is up to you

- **What your commands do to the device together.** dutagent does not know what
  a command does to the DUT. A power cycle while a console is open is fine: the
  console shows the reboot. A power cycle during a flash write can ruin the
  flash.
- **Order.** Commands side by side run in no set order. Steps that must happen
  in a fixed order belong in one command.
- **Shared hardware.** Two of your commands that use one serial port, GPIO pin,
  PDU outlet or BMC can overlap. On a serial port the second one fails with
  `Serial port busy` when it opens the port, which may be after earlier steps of
  its command ran: a `boot` command that power-cycles and then opens the console
  cuts the power before it fails beside an open console. Pins, outlets and BMCs
  are not caught at all.
- **A serial adapter the DUT powers** disappears with the power, and the console
  ends with `Port has been closed`. Open it again once the DUT is up.
- **What runs under your name.** Everything you start under one name may run
  side by side.

## When the reservation ends while commands run

| The reservation … | Your running commands | Your next command | Other users |
| --- | --- | --- | --- |
| expires, or you `unlock` | go on | as before | refused until your last command has ended; then the device is free |
| is ended by someone's `unlock force` | go on | as before | the same |
| ends because dutagent aborts its work | are aborted | refused | refused |

## When a device stays in use

A command holds its device until it returns, and nobody else can end it:
neither `unlock force` nor an expired reservation does.

- `dutctl list` shows who holds a device.
- A console left open in a forgotten terminal holds the device until it is
  closed: ask its owner to end it (Ctrl-A x).
- If your own client lost its connection, dutagent ends the command once it
  notices, which can take minutes; until then a retry of the same command is
  refused.
- A module that does not return after a cancel keeps the device until the admin
  restarts dutagent, and the agent log warns every 10 s.

## In scripts and CI

Run commands side by side in a script only when one must span the others, such
as a serial log across a power cycle; otherwise configure one command. Call
dutctl directly, so that `$!` is dutctl's process, and let the background
command end by itself:

```sh
set -eu
U="ci-${CI_JOB_ID:?}@runner"; A=agent.lab:2024
dutctl -s "$A" -u "$U" dev lock 1h
trap 'dutctl -s "$A" -u "$U" dev unlock || true' EXIT
dutctl -s "$A" -u "$U" dev serial -t 3m -- expect 'login:' > serial.log 2>&1 &
mon=$!
for i in $(seq 100); do               # at most 20 s
  grep -q -- '--- Connected to' serial.log && break
  kill -0 "$mon" 2>/dev/null || { cat serial.log; exit 1; }
  sleep 0.2
done
grep -q -- '--- Connected to' serial.log
dutctl -s "$A" -u "$U" dev power off
sleep 2
dutctl -s "$A" -u "$U" dev power on
wait "$mon"                           # 0 once 'login:' was seen; 1 on timeout or any failure
```

Do not end a background dutctl with a signal (`kill`, Ctrl-C) before the next
step: dutctl returns at once while the agent still stops the command, and a next
step that runs the same command is refused.

## For admins

- Run dutagent as a user of its own, never as root, as the packaged unit does:
  only then does a second open of a serial port fail. As root, two commands
  share the port's bytes.
- A user's commands can run at the same time. Commands that use one serial
  port, GPIO pin, PDU outlet, BMC or upload destination can therefore overlap;
  where an overlap would hurt, give that hardware to one command, and keep
  steps that must run in order in one command.
- Keep the console and the power control in separate commands, so users can run
  them side by side.
- A command never runs twice at once: if two people or jobs need the same
  function at once, configure it twice under two names.

See [dutagent-shutdown.md](./dutagent-shutdown.md) for how commands and the
agent end.
