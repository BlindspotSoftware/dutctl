# System Overview

DUT Control (DUTCTL) is a decentralized client-agent architecture as shown here:

![dutctl_server_agent](https://github.com/BlindspotSoftware/dutctl/assets/14163031/c16b0bde-4fb1-4a4e-8faf-ff63e24d8ac8)

Multiple Devices-Under-Test (DUTs) can be connected and physically wired to one DUT Agent (DA) which performs the
hardware interaction. If the system scales, multiple DUT Agents can be used. Users control DUTs through DUT Client,
which connects (remotely) to a DUT Agent and builds the system's user interface. 

In a future release, there will be DUT Server, which abstracts the DUT Client to DUT Agent connections and improves the
usability in larger systems. From the DUT Client side, there is no difference between talking to a DUT Agent or the DUT
Server in terms of controlling the hardware.

## Device-Under-Test (DUT)
The machine or hardware you want to operate.

## DUT Client (dutctl)
This is the actual application running on the user's machine. It provides a command line interface to issue a task.
This client app, thought, has no knowledge about the connected DUT's and their available control operations. That
information is provided by the agent on request. 

## DUT Agent (DA)
The DUT Agent is a service designed to run on a single board computer, which can handle the wiring to the DUT (power
control, reset, flasher, serial console, etc.) The specifics and supported operation for the wired DUTs are feed to the
DUT Agent via a [configuration file](./dutagent-config.md)

## DUT Server
The DUT Server is designed to let the project scale. Its basic purpose is to maintain a table with the DUT to DUT Agent
relations. Its interface towards a DUT Client is the same as the one from a DUT Agent. This way there is no difference
from the client side to which instance to talk to. Additionally, the DUT Server could expose further interfaces like a
REST API to observe the fleet of DUTs. 

# Communication Design

The distributed entities of the DUT Control system communicate via Remote Procedure Calls (RPCs), which are defined in
`protobuf/dutctl/v1/dutctl.proto`. The communication is always initiated by the client, and there are three calls 
defined in the RPC service that a client can issue to the agent: 
1) List, to list the available connected devices 
2) Commands, to learn about the available commands of a given device
3) Run, to execute a command on a device

While the 1) and 2) are quite straight forward, the Run-RPC is a bidirectional stream, where both the client and the
agent are sending multiple messages until the end of the command execution. According to the protobuf definition, during
a Run-RPC stream, the client and the agent are sending RunRequests and RunResponses, respectively. These messages are
abstractions for different types of messages being sent between client and agent, and the following convention applies:

The first RunRequest sent by the client must always be a Command message. Depending on the module implementation of the
executed command, there are the following scenarios for the further communication during the Run-RPC stream: 

![print-msg](https://github.com/user-attachments/assets/e2f0b21e-3048-44d4-81e1-aab58017c38d)

**Print**: After the initial RunRequest with a Command message by the client, the agent sends one or many RunResponses
being Print messages. This type of messages is usually good for status updates of basic commands, which do not require
further interaction or input. By convention, Print messages should not be mixed with Console messages.

![Console-msg](https://github.com/user-attachments/assets/e1a946bf-3482-41c1-9a01-5df5d5318fc7)

**Console**: After the initial RunRequest with a Command message by the client, the agent sends one RunResponses being
a Console message. From this time on until the end of the command execution, standard input from the client is
redirected to the agent and standard output and standard error from the agent to the client. This way a remote console
is realized, which enables interactive command execution. By convention, Console messages should not be mixed with Print
messages.

![FileDownload-msg](https://github.com/user-attachments/assets/2e6d75e6-02b0-43e1-875f-3e7634b6b147)

**File download to the client**: After the initial RunRequest with a Command message by the client, for commands
producing any artifacts, these can be downloaded to the client, with a RunResponse being a File message. Downloads can
happen multiple times and can be mixed with Print messages and Console messages and file uploads.

![FileUpload-msg](https://github.com/user-attachments/assets/1a12204b-58b1-4b05-88ec-c8a3ba3f2b6a)

**File Upload to the agent**: After the initial RunRequest with a Command message by the client, for commands needing
any artifacts, these can be uploaded to the client, with a RunResponse being a FileRequest message and the client
answering with a RunRequest being a File message. Uploads can happen multiple times and can be mixed with Print
messages and Console messages and file downloads.

