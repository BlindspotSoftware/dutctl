# System Overview

DUTCTL is a decentralized client-agent architecture as shown here:

![dutctl_server_agent](https://github.com/BlindspotSoftware/dutctl/assets/14163031/c16b0bde-4fb1-4a4e-8faf-ff63e24d8ac8)

Multiple Devices-Under-Test (DUTs) can be connected and physically wired to one DUT Agent (DA) which performs the hardware interaction. If the system scales, multiple DUT Agents can be used. DUT Controll are clients that connect (remotely) to a DUT Agent and build the system's user interface. 

As a later feature there will be DUT Server which abstracts the DUT to DUT Agent connections and improves the usability in larger systems. From the DUT Controll client side there is no difference between talking to a DUT Agent or the DUT Server in terms of controlling the hardware.

## Device-Under-Test (DUT)
The hard test you want to operate.

## DUT Agent (DA)
The DUT Agend is a service designed to run on a single board comuter, which can handle the wiring to the DUT (power control, reset, flasher, serial console, etc.) The specifics and supported operation for the wired DUTs are feed to the DUT Agent via a [configuration file](./dutagent-config.md)

## DUT Controll (dutctl)
This is the actual application running on the user's machine. It provides a command line interface to issue task. 

## DUT Server

