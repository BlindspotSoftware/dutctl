# About device access

For users of dutctl: who can use a device when, what dutagent takes care of, and
what is up to you when you run several of your commands on one device at once.
The [access rules](./access-rules.md) list what is refused when, word for word.

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
the same device at once, unless a script runs under a name of its own:
[Use dutctl in scripts and CI](./use-dutctl-in-ci.md) shows how.

## Reservations

`dutctl <device> lock [duration]` reserves a device for you, for 30 minutes
unless you give a duration; `lock` again extends it. While it lasts, nobody else
can start a command on the device or reserve it. `dutctl <device> unlock` ends
it. `dutctl <device> unlock force` ends anyone's reservation, for one its owner
forgot, but never a running command. Reserve a device for a session of several
commands: between two of your commands, a device you have not reserved is free
for anyone.

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

See [About the agent's lifecycle](./dutagent-shutdown.md) for how commands and the
agent end.
