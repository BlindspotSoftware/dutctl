
# DUT Control: Unified Device Management for Open Firmware Development

_dutctl_ stands for "Device-under-Test Control" and is an open-source command-line utility and service ecosystem for managing development and test devices in firmware environments. By providing a unified interface to interact with boards and test fixtures across platforms, _dutctl_ eliminates the fragmentation of device management tools that has long plagued firmware workflows. The project features remote device control, command streaming, multi-architecture testing, and a flexible plugin architecture for extensibility.

[![GitHub Release](https://img.shields.io/github/v/release/BlindspotSoftware/dutctl?include_prereleases&sort=semver&display_name=release)](https://github.com/BlindspotSoftware/dutctl/releases)
![Build Status](https://img.shields.io/github/actions/workflow/status/BlindspotSoftware/dutctl/go.yml?branch=main)
[![License](https://img.shields.io/github/license/BlindspotSoftware/dutctl)](https://github.com/BlindspotSoftware/dutctl/blob/main/LICENSE)

**Documentation: <https://blindspotsoftware.github.io/dutctl/>**


---

## Overview

_dutctl_ is designed for firmware developers, QA teams, and CI/CD pipelines, simplifying complex device interactions 
through intuitive commands, easy configuration, and scalability. The platform supports both local and remote device 
management through its agent-server architecture, enabling efficient collaboration in distributed teams. 

```
┌────────────┐        ┌────────────┐        ┌───────────┐
│ Client     │        │ Agent      │        │ Device-   │
│ (e.g.      │ Network│ (e.g. RPi4)│  Wired │ under-    │
│ Laptop)    │───────▶│            │───────▶│ Test      │
└────────────┘  RPC   └────────────┘        └───────────┘
                
```

Key features include:
- Unified command interface for diverse devices
- Remote device management and command streaming
- Multi-architecture and distributed testing
- Extensible plugin system for new hardware and protocols

For detailed information on the system architecture, see [About the architecture](./docs/architecture.md).

| Supported Client OS | Recommended DUT Agent Hardware |
| ------------------- | ------------------------------ |
| Linux               | RaspberryPi 4                  |

## Getting Started

Download the [latest release](https://github.com/BlindspotSoftware/dutctl/releases) or use the 
[go toolchain](https://go.dev/) to install the components:

<!-- x-release-please-start-version -->
```bash
go install github.com/BlindspotSoftware/dutctl/cmds/dutctl@v1.0.0-beta.2
go install github.com/BlindspotSoftware/dutctl/cmds/dutagent@v1.0.0-beta.2
```
<!-- x-release-please-end -->


To try it on your machine without any hardware, follow [Get started](./docs/getting-started.md): it runs an agent
with a simulated board, and walks you through controlling it with dutctl.

## Release Plan

_dutctl_ is on its way to a stable v1.0.0. Before promising backwards compatibility, we use two pre-release phases
to validate in real-world use that we got the idea of the public interfaces right:

- **Alpha** (released as `v1.0.0-alpha.N`) — focus on automated use.
  A CI team stresses the features relevant for automated operation of _dutctl_, e.g. in test pipelines and scripted
  workflows. The automation surface is the smaller one to harden, so it comes first.

- **Beta** (current, released as `v1.0.0-beta.N`) — focus on interactive use.
  Once automated operation proves stable, a team of firmware developers additionally uses _dutctl_ in their daily,
  interactive work with their devices.

- **Stable** (`v1.0.0`) — the primary public interfaces below become subject to the backwards-compatibility promise.

During the alpha and beta phases, releases are cut monthly at the latest — earlier when major features, fixes, or
interface changes land. Expect the interfaces to still change in response to testing feedback; changes are announced
in the release notes.

## Public Interfaces

The project's public interfaces fall into two tiers:

**Primary — the API contract.** These interfaces will become stable and backwards compatible with v1.0.0:

1) DUT Agent configuration grammar:
   - See the [YAML specification](./docs/dutagent-config.md)

2) RPC communication protocol for interacting with agents:
   - See the [Protobuf definitions](./protobuf/dutctl/v1/dutctl.proto)
   - Not covered: the experimental [relay protocol](./protobuf/dutctl/relay/v1alpha1/relay.proto), with which agents
     register with a DUT Server, and the Go code generated from it

3) Module API for extending the DUT Agent with your own modules:
   - See the [module guide](./docs/module_guide.md) and the Go package [`pkg/module`](./pkg/module)

**Secondary — best effort.** These interfaces are kept stable where possible, but are not covered by the strict
compatibility promise:

- Command-line interfaces for the project's applications:
   - DUT Client - see `dutctl -h`
   - DUT Agent - see `dutagent -h`
   - DUT Server (in development, currently at proof-of-concept stage)

## Individual Setup

If you are ready to get your hands dirty, hook up your DUT to a single board computer (we recommend RPi4) and launch the
DUT agent on it. Read on [here](./docs/dutagent-config.md) to learn how you can adapt the agent's configuration to your
needs, with the [modules](./pkg/module) that dutagent ships with: each describes its options in its README, and the
[documentation](https://blindspotsoftware.github.io/dutctl/reference/modules/) lists them all.

If you have special needs, you can extend the system with your own modules. Read about the [module plugin system](./docs/module_guide.md).

## DUT Control at Scale

If you have multiple devices hooked up to multiple agents at different locations, you may wonder if you can handle them
without connecting to the respective agents every time. [DUT server](./cmds/exp/dutserver/README.md) is there for the
rescue: It is at proof-of-concept state right now, but you can already [try it out](./cmds/exp/dutserver/README.md).

## Contributing

Contributions are welcome! Please see our [Contributing Guide](./CONTRIBUTING.md) for details on how to get involved.


---

This project is supported by the [NLnet Foundation](https://nlnet.nl/) and the Next Generation Internet (NGI) Zero
Commons Fund. The NGI0 Commons Fund is made possible with financial support from the European Commission's
[Next Generation Internet](https://ngi.eu/) program.

|                              |                                    |                            |
| ---------------------------- | ---------------------------------- | -------------------------- |
| ![nlnet](./assets/nlnet.png) | ![European Union](./assets/EU.png) | ![NGI0](./assets/NGI0.png) |

