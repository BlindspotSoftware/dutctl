# System Overview

DUTCTL is a dezentralized client-server design as shown here:

![dutctl_server_agent](https://github.com/BlindspotSoftware/dutctl/assets/14163031/c16b0bde-4fb1-4a4e-8faf-ff63e24d8ac8)

Devices under test(DUTs) are connected to a single board computer (e.g. Raspberry PIs) which provides the service of DUT Agent.
One DA can handle multiple DUTs.
Communication and Control between a DA and DUT is specific to the DUT.
User can access the service of a DA via the commandline tool called `dutctl`.
As communication protocol between `dutctl` and DA, `grpc` is used.

In a later state of the project, a command and control server shall be implemented to bundle multiple DAs and allow managing them with `dutctl` the same way.
The command and control server shall provide additional capabilities, e.g. user specific access control, statistics, firmware upload and download, and more.

## Device under test (DUT)
A DUT is a mainboard/CPU combination.
For this device, firmware shall be developed and tested.
To interact with the device, it offers physical connectors/interfaces.
Firmware needs to be copies on a flash chip or onto a flash emulator attached to the mainboard to be executed on the hardware.
To get the system to start, we need it to power cycle.
After a power cycle firmware is being executed.
Usually information about the boot process is delivered over a serial interface, which is accessible via a physical connector on the mainboard.

## DUT Agent (DA) - Server
The DUT Agent is a service usually running on a single board comuter and handles operations(power supply, reset, flasher, serial console, GPIO etc.) on an attached device under test.
The specifics and supported operations of the DUTs can differ on hardware and software.
To allow the DUT Agent the flexibility to interact with different hardware/software designs of a DUT, a [configuration file](./dutagent-config.md) is fed to DUT Agent, which defines devices, operations and modules.

### Modules
Modules provide the means to a DUT Agent to interact with a certain capability of a DUT, e.g. power cycle, reading and writing to a serial interface, flashing new firmware on a specific flash emulator.
Modules are bound to a command in the configuration file and is invoked when the command is issued from a client.

## DUT Control (dutctl) - Client
Dut Control is the client application which allows to issue commands to a DUT Agent.
It is realized as a commandline interface application with the requirement to minimize logic for interaction with a server.
In case of access to the serial interface of a DUT, dutctl receives the byte stream from the server and delievers any input to the server.
The handling of these streams is the duty of DUT Agent.

## DUT Server
Not yet defined
