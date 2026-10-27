---
status: accepted
date: 2024-07-08
---

# Split DUT control into an agent and a thin client

## Context and problem statement

Firmware developers need to control boards remotely: switch the power, press
reset, flash firmware and watch the serial console. The wiring for this ends at
a machine next to the devices under test (DUTs), while the developers work on
their own machines. One such machine can serve several DUTs, and a growing lab
needs more of them.

The project had to decide which part of the system knows the devices, and how a
user's tool reaches them.

## Decision drivers

- The hardware interaction must run on the machine that is wired to the DUTs.
- Users work remotely, from their own machines.
- A lab grows by adding machines; later, a server should hide which machine
  serves a device.

## Decision outcome

DUT Control is a client-agent system.

- dutagent runs on the Linux machine wired to the DUTs. It is designed for a
  single-board computer such as a Raspberry Pi 4. It is the only component that
  knows the devices, their commands and the modules behind them. It reads them
  from its [configuration file](../dutagent-config.md).
- dutctl is the command-line client on the user's Linux machine. It has no
  knowledge of the devices: it asks the agent for the devices, their commands
  and their help. Every exchange between client and agent starts with a client
  request.
- One agent can serve several DUTs. A lab scales by adding agents.
- A DUT server can sit between clients and agents. It offers clients the same
  interface as an agent, so a client talks to both in the same way.

### Consequences

- Good, because one client works with every agent: what a device can do is
  configured once, on its agent, and discovered at run time.
- Good, because a server with the agent's interface can sit in front of many
  agents without changes to the client.
- Good, because the client keeps no state of its own: an invocation makes at
  most one RPC and exits.
- Bad, because nothing works without a reachable agent, not even the help text
  of a command.
- Neutral, because the agent configuration becomes the place where users
  describe their lab, so its grammar is a public interface (see
  [ADR-0005](0005-configure-devices-and-commands-in-one-yaml-file.md) and
  [ADR-0028](0028-tier-the-public-interfaces-for-v1-0-0.md)).

## More information

- [docs/README.md](../README.md): the system overview.
- [ADR-0002](0002-speak-grpc-over-cleartext-http-2-with-connect-go.md): how client and agent talk.
- [ADR-0003](0003-run-a-command-over-one-bidirectional-stream.md): how a command runs
  between client and agent.
- [ADR-0024](0024-keep-dutctl-a-hardware-control-layer.md): what dutctl leaves
  to the tools on top of it.
- [cmds/exp/dutserver](../../cmds/exp/dutserver): the experimental DUT server.
- Later changes:
  - 2025-07: the experimental relay dutserver implemented the DUT server as a
    proof of concept. It answered List from its own registry and forwarded
    Commands, Details and Run to the agent that owns the device. Agents
    started with its address registered their devices with it, with no
    handshake and no renewal. Locks stayed with the agents: the lock calls
    added later were not added to the relay.

Recorded retroactively in October 2026.
