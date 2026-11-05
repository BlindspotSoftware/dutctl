---
status: accepted
date: 2026-10-20
---

# Run a user's different commands side by side

## Context and problem statement

[ADR-0030](0030-run-one-command-at-a-time-per-device.md) made a device run one
command at a time, even for its owner: steps that belong together were to be
one command. The maintainers accepted that a user could no longer power-cycle a
device from a second terminal while their own console on it was open. The
interactive console of [ADR-0035](0035-frame-consoles-in-line-or-raw-mode.md)
makes this a real loss. A person types into it for as long as needed, and a
power cycle needed meanwhile cannot be folded into the same command.

Two hazards stand against admitting a user's runs again. Runs that share one
busy hold free the device when the first of them ends. And a command's module
instances are created once, when the agent loads its configuration, and serve
every run of the command
([ADR-0009](0009-initialize-every-module-once-at-agent-start.md)). Some modules
keep per-run state in their fields: `flash` stores the operation in `Run` and
builds the tool's command line from it later. A `flash read` beside a
`flash write` of the same command could run as a write.

Which of a user's commands may run on a device at once, and what does the agent
check?

## Considered options

- **Different commands side by side**: no opt-in, but never the same command
  twice; each run holds the device through a lease of its own.
- **One command per device**: keep the rule of ADR-0030.
- **An opt-in per call**: a flag on the call admits a command beside the
  user's running ones.
- **A reservation as the opt-in**: a user's commands overlap only on a device
  the user has reserved.
- **Every command of the user**: the same command twice included, with every
  module made safe for concurrent runs.
- **Shared hardware caught in code**: modules or the configuration declare the
  ports, pins and outlets a command uses, and the agent refuses overlaps.

## Decision outcome

Chosen option: **different commands side by side**, because it allows a console
beside a power cycle without a flag or a protocol change. The agent still
enforces what only it knows: who holds a device, and which module instances
are in use. What the commands do together stays with the user and the admin.

One command per device rules out that workflow, and an opt-in per call is more
than the case needs. With a reservation as the opt-in, `lock` would mean both
"keep others out" and "my commands may overlap", and an expiring reservation
would withdraw the opt-in mid-session. Making every module safe for concurrent
runs is much more work, now and for every future module. Shared hardware is
already the admin's responsibility, as with two commands that switch the same
GPIO pin.

The agent refuses a run when:

- another owner has reserved the device or runs a command on it;
- the agent is stopping, except that while it drains, it still admits runs on
  a device the caller has reserved
  ([ADR-0032](0032-stop-the-agent-in-stages.md));
- the same command, by device and command name, whatever its arguments, still
  runs or is still stopping after a cancel.

Every other run is admitted, without an opt-in.

- Each run holds the device through a lease of its own, which only that run
  ends, once its modules have returned. A forced unlock, the end of a
  reservation and the agent's abort stage leave leases alone, and the device
  stays busy until its last lease ends. Counting runs on one shared hold,
  weighed for ADR-0030, would need a counter and a generation number; a list
  of leases gives the count and tells the runs apart.
- All leases of a device belong to one owner, so `dutctl list` still shows one
  holder. The protocol and the client stay as they are; a run refused as the
  same command gets a message that names it.
- What a user's commands do to the device together is the user's
  responsibility: a power cycle beside an open console is fine, beside a flash
  write it can ruin the flash. Commands side by side run in no set order.
- Hardware that two commands share, such as a serial port, a GPIO pin, a PDU
  outlet or a BMC, is the admin's responsibility; the agent does not check it.
  The serial library opens ports for exclusive use, so a second command fails
  when it opens a port in use. That happens only after the modules before it
  have run, and not at all for an agent with the `CAP_SYS_ADMIN` capability,
  as when it runs as root.
- Everything started under one name may run side by side, CI jobs included. A
  job that must stay apart uses a name of its own with `-u`.

Access to memory-mapped GPIO is serialized for this. The GPIO modules map the
registers before every operation and unmap them after it, and the mapping is
shared by the whole process, so one operation could unmap the registers while
another used them, which could crash the agent.

### Consequences

- Good, because a user can keep a console open and power-cycle the device from
  a second terminal.
- Good, because no run ends another run's hold, and a module instance still
  serves one run at a time.
- Bad, because the agent no longer keeps a user's commands apart: commands that
  drive the same hardware can overlap, and only a second open of a serial port
  is caught, late.
- Bad, because a command that two terminals or jobs under one name need at
  once has to be configured twice, under two names.
- Bad, because an opt-in added later would restrict what users got used to.

## More information

- Supersedes [ADR-0030](0030-run-one-command-at-a-time-per-device.md).
  [ADR-0031](0031-keep-a-device-busy-until-its-modules-return.md) stays in
  force: like the busy hold, a lease ends only once its run's modules have
  returned.
- [ADR-0009](0009-initialize-every-module-once-at-agent-start.md) and
  [ADR-0035](0035-frame-consoles-in-line-or-raw-mode.md): the shared module
  instances and the console that shape this decision.
- [ADR-0017](0017-lock-devices-in-agent-memory.md) and
  [ADR-0018](0018-identify-callers-by-the-http-from-header.md): reservations,
  and owners as self-asserted names, `<user>@<host>` by default.
- [ADR-0032](0032-stop-the-agent-in-stages.md): a stopping agent waits for
  every lease, as it waited for every busy device.
- [internal/dutagent/locker](../../internal/dutagent/locker) and
  [cmds/dutagent/run.go](../../cmds/dutagent/run.go) implement the rule;
  [docs/device-access.md](../device-access.md) describes it for users and
  admins.
