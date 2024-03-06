# System Overview

DUTCTL is a dezentralized server-agent design as shown here:

![dutctl_server_agent](https://github.com/BlindspotSoftware/dutctl/assets/14163031/c16b0bde-4fb1-4a4e-8faf-ff63e24d8ac8)

## DUT
The device unter test you want to operate.

## DUT Agent (DA)
The DUT Agend is a service usually running on a single board comuter, which can handle the wireing to the DUD (power supply, reset, flasher, serial console, GPIO etc.) The specifics and supported operation for the wired DUTs are feed to the DA via a [configuration file](./dutagent-config.md)

## DUT Controll (dutctl)
This is the actual application running on the userser machine. It provides a command line interface to issue task. 

## DUT Server
