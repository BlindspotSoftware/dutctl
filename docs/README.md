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

# Transport Security

RPCs are carried over TLS by default, in every direction: DUT Client to DUT Agent, DUT Client to DUT Server, DUT Server
to DUT Agent, and the DUT Agent's registration call to the DUT Server.

**What this protects, and what it does not.** The DUT Agent and DUT Server serve a self-signed certificate, which they
generate on first start if none is present. No peer verifies the certificate it is offered. The connection is therefore
**encrypted but not authenticated**: it stops passive eavesdropping on the wire, but not an active man-in-the-middle.
There is no client authentication either — any client that can reach an agent may talk to it, exactly as before. Supplying
your own CA-issued certificate via `-tls-cert`/`-tls-key` does not change this today, because no client in the project
verifies what it is offered.

**Both ends must agree.** The transport is a deployment-wide setting, not something the peers negotiate: a mismatch fails
below the RPC layer, before any headers are exchanged, so it cannot be reported as a protocol error. `dutctl` recognises
the two mismatch cases and prints a hint naming the fix.

| Flag | Applies to | Effect |
| --- | --- | --- |
| `-insecure` | `dutctl`, `dutagent`, `dutserver` | Use plain HTTP/2 cleartext (h2c). All participants must be started with it. |
| `-tls-cert` | `dutagent`, `dutserver` | Path to the certificate. A self-signed pair is generated if neither it nor the key exists. |
| `-tls-key` | `dutagent`, `dutserver` | Path to the private key, written mode `0600`. |

**Generated key pairs.** `dutagent` defaults to `/var/lib/dutagent/tls/{cert,key}.pem` and `dutserver` to
`/var/lib/dutserver/tls/{cert,key}.pem` — under `/var/lib` rather than `/etc` because the packaged systemd unit runs
unprivileged with `ProtectSystem=strict`. Generation happens only when *neither* file exists; if one is present the pair
is loaded as-is and a load failure is reported rather than silently overwritten. Nothing rotates a certificate, and
expiry is never checked, since no peer verifies it.

**Upgrading.** This is a breaking change. A `dutctl` from before this release cannot talk to an upgraded agent, and an
upgraded `dutctl` cannot talk to an older agent. Either upgrade both ends together, or run every participant with
`-insecure` to keep the previous cleartext behaviour until you can.

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

