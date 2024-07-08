# System Overview

DUTCTL is a decentralized client-agent architecture as shown here:

![dutctl_server_agent](https://github.com/BlindspotSoftware/dutctl/assets/14163031/c16b0bde-4fb1-4a4e-8faf-ff63e24d8ac8)

Multiple Devices-Under-Test (DUTs) can be connected and physically wired to one DUT Agent (DA) which performs the hardware interaction. If the system scales, multiple DUT Agents can be used. DUT Controll are clients that connect (remotely) to a DUT Agent and build the system's user interface. 

As a later feature there will be DUT Server which abstracts the DUT to DUT Agent connections and improves the usability in larger systems. From the DUT Controll client side there is no difference between talking to a DUT Agent or the DUT Server in terms of controlling the hardware.

## Device-Under-Test (DUT)
The machine or hardware you want to operate.

## DUT Control (dutctl)
This is the actual application running on the user's machine. It provides a command line interface to issue task. This client app thought, has no knowledge about the connected DUT's and their available controll operations. Those information is provided by the agent on request. 

## DUT Agent (DA)
The DUT Agend is a service designed to run on a single board computer, which can handle the wiring to the DUT (power control, reset, flasher, serial console, etc.) The specifics and supported operation for the wired DUTs are feed to the DUT Agent via a [configuration file](./dutagent-config.md)

## DUT Server
The DUT Server is designed to let the project scale. It's basic purpose is to maintain a table with the DUT to DUT Agent relations. It's interface towards a DUT Control client is the same as the the one from a DUT Agent. This way there is no difference from the client side to which instance to talk to. Additionally the DUT Server could expose further interfaces like a REST API to observe the fleet of DUTs. 

