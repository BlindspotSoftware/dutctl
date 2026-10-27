---
status: accepted
date: 2026-07-22
---

# Keep dutctl a hardware-control layer

## Context and problem statement

dutctl came out of testing firmware on real machines. An agent next to the
hardware offers named commands per device, such as flashing, power and the
serial console, and keeps one lock per device. People at the bench, scripts
and CI systems call these commands through the client or the RPC API.

Feature requests began to reach beyond that. One asked the agent to power a
device off after a period without use; the discussion proposed a configured
list of commands that the agent runs once no command has come for a set time.
Another asked to keep known-good firmware images on the agent, with shorthands
to flash them. The project had to decide where dutctl ends and where the tools
on top of it begin.

## Decision outcome

dutctl is the layer at the hardware, and nothing more.

- An agent offers named commands for each of its devices, built from modules,
  the calls to discover them, and one lock per device.
- dutctl has no test framework, no job scheduler or queue, no idle power-off
  policy, and no management of firmware images. These belong to the tools on
  top, such as a CI system, a script or a person at the bench. FirmwareCI, for
  example, schedules its jobs itself and reserves machines through the locks.
- When a tool on top needs more information from the agent, dutctl can provide
  it through the RPC API, for example more device state in the List call behind
  `dutctl list`, rather than take over the tool's policy.

The request for idle power-off was closed as belonging to a higher layer that
uses dutctl. The request for known-good images stayed open for design; the
first answer suggested that it belongs to the user's domain.

### Consequences

- Good, because any tool can build on the same commands and locks, and dutctl
  does not tie a lab to one CI system.
- Bad, because a lab without a tool on top has no idle power-off, no queue for
  busy devices and no store of known-good images.
- Neutral, because requests at the edge still need a design discussion each,
  as the one about known-good images shows.

## More information

- [ADR-0001](0001-split-dut-control-into-an-agent-and-a-thin-client.md): the agent owns the devices, the
  client is thin.
- [ADR-0017](0017-lock-devices-in-agent-memory.md): the lock per device.
- In October 2026, a request for an endpoint that exposes the agent's
  configuration was closed with a pointer to the existing Details RPC.

Recorded retroactively in October 2026.
