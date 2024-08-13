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

# Communication Design

The distributed entities of the DUT Control system comunicate via Remote Proceture Calls (RPCs), which are defined in `protobuf/dutctl/v1/dutctl.proto`. The communication is always initiated by the client and there are three calls defined in the RPC service, that a client can issue to the agent: 
1) List, to list the the available connected devices 
2) Commands, to learn about the available commands of a given device
3) Run, to execute a command on a device

While the 1) and 2) are quite straight forward, the Run-RPC is a bidirectional stream, where both, the client and the agent are sending multiple messages until the end of the command execution.
According to the protobuf definition, during a Run-RPC stream the client and the agent are sending RunRequests and RunResponses, respectively. This messages are abstractions for different types of messages being sent between client and agent and the following convention applies:

The first RunRequest sent by the client must always be a Command message. 
Depending on the module implementation of the executed command, there are the following scenarios for the further communication during the Run-RPC stream: 

**Print**: After the initial RunRequest with a Command message by the client, the agent sends one or many RunResponses being Print messages. This type of messages are usually good for status updates of basic commands, which do not require further interaction or input. By convention Print messages should not be mixed with Console messages.

**Console**: After the initial RunRequest with a Command message by the client, the agent sends one RunResponses being a Console message. From this time on until the end of the command execution, standard input from the client is redirected to the agent and standard output and standard error from the agent to the client. This way a remote console is realized, which enables interactive command execution. By convention, Console messages should not be mixed with Print messages.

**File download to the client**: After the initial RunRequest with a Command message by the client, for commands producing any artifacts, these can be downloaded to the client, with a RunResponse being a File message. Downloads can happen multiple times and can be mixed with Print messages and Console messages and file uploads.

**File Upload to the agent**: After the initial RunRequest with a Command message by the client, for commands needing any artifacts, these can be uploaded to the client, with a RunResponse being a FileRequest message and the client answering with a RunRequest being a File message. Uploads can happen multiple times and can be mixed with Print messages and Console messages and file downloads.

