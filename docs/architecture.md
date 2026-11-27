# About the architecture

DUT Control (dutctl) is a decentralized client-agent architecture as shown here:

![dutctl_server_agent](https://github.com/BlindspotSoftware/dutctl/assets/14163031/c16b0bde-4fb1-4a4e-8faf-ff63e24d8ac8)

Multiple Devices-Under-Test (DUTs) can be connected and physically wired to one DUT Agent (DA) which performs the
hardware interaction. If the system scales, multiple DUT Agents can be used. Users control DUTs through DUT Client,
which connects (remotely) to a DUT Agent and builds the system's user interface.

In a future release, there will be DUT Server, which abstracts the DUT Client to DUT Agent connections and improves the
usability in larger systems. From the DUT Client side, there is no difference between talking to a DUT Agent or the DUT
Server in terms of controlling the hardware.

Why the system is built this way, and which options were weighed, is recorded in the
[decision records](./decisions/README.md).

## Device-Under-Test (DUT)

The machine or hardware you want to operate.

## DUT Client (dutctl)

This is the actual application running on the user's machine. It provides a command line interface to issue a task.
This client app, though, has no knowledge about the connected DUTs and their available control operations. That
information is provided by the agent on request. Who can use a device when, and what is up to you when you run several
commands on one device at once, is described in [About device access](./device-access.md).

Run without arguments on a terminal, dutctl opens a TUI (terminal user interface) that walks from hosts to devices to
commands, fetching each level from the agent, and remembers your recent devices in a history file of its own; see
[the TUI](./dutctl-tui.md). In a pipe or a script, a bare dutctl lists the devices.

## DUT Agent (DA)

The DUT Agent is a service designed to run on a single board computer, which can handle the wiring to the DUT (power
control, reset, flasher, serial console, etc.) The specifics and supported operation for the wired DUTs are fed to the
DUT Agent via a [configuration file](./dutagent-config.md). How the agent and its commands end, on stop signals,
client cancellation and failures, is described in [About the agent's lifecycle](./dutagent-shutdown.md).

## DUT Server

The DUT Server is designed to let the project scale. Its basic purpose is to maintain a table with the DUT to DUT Agent
relations. Its interface towards a DUT Client is the same as the one from a DUT Agent. This way there is no difference
from the client side to which instance to talk to. Additionally, the DUT Server could expose further interfaces like a
REST API to observe the fleet of DUTs.

## Devices, commands and modules

The configuration of an agent mainly consists of a list of devices connected to it and a list of the _Commands_
available for those devices. Commands are meant to be the high-level tasks you want to perform on the device, e.g.
"Flash the firmware with the given image." To achieve this high-level task, commands can be built up of one or multiple
_Modules_. Modules represent the basic operations and represent the actual implementation for the hardware interaction.
The implementation of a Module determines its capabilities and also exposes information on how to use and configure it.

The DUT Control project offers a collection of [module implementations](../pkg/module) but also allows for easy
integration of [custom modules](./module_guide.md). Often a _Command_ can consist of only one _Module_ to get the job
done, e.g., power cycles the device. But in some cases like the flash example mentioned earlier, eventually it is
mandatory to toggle some GPIOs before doing the actual SPI flash operation. In this case the command is built up of a
Module dealing with GPIO manipulation and a Module performing a flash writing with a specific programmer.

## How client and agent talk

The communication is always initiated by the client, through remote procedure calls (RPCs): to list the devices, to
learn the commands of a device and their help, to reserve a device, and to run a command. Running a command is a
stream in both directions,
over which a module prints messages, opens a console for the user's terminal, or moves files between the user's
machine and the agent. The [RPC protocol](./rpc-protocol.md) describes the calls and their messages.
