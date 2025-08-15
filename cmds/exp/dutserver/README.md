# DUT Server - Proof of Concept

> [!IMPORTANT] 
> This application is in an experimental state. It is intended as a technology demonstration and is not yet ready for production use.
> Cmdline API and functionality may change.


The DUT Server acts as a centralized proxy for multiple Device Under Test (DUT) agents, enabling remote access to devices from any location. This server component is designed to sit between clients (using `dutctl`) and device agents (running `dutagent`), creating a unified access layer for device management.

```
┌─────────┐      ┌───────────┐      ┌─────────┐      ┌────────┐
│ dutctl  │◄────►│ dutserver │◄────►│ dutagent│◄────►│ Device │
│ (client)│      │ (proxy)   │      │         │      │        │
└─────────┘      └───────────┘      └─────────┘      └────────┘
```

The main goal of the DUT Server is to provide a single access point for all registered devices, regardless of their physical location, i.e., regardless of the DUT Agent devices are connected to.

## Implemented Features

- **Device Registration:** Agents can register their devices with the server.
- **Device Discovery:** Clients can list all available devices on the server.
- **Command Forwarding:** Transparently forwards commands from clients to the appropriate device agent.
- **Bidirectional Streaming:** Supports real-time command execution with bidirectional data streaming.

## Current Limitations

As a proof of concept, this implementation has several limitations that would need to be addressed for production use:

- **No Persistence:** Registered devices are not persisted between server restarts.
- **No Agent Health Monitoring:** Lacks handshake or keep-alive mechanisms for registered agents.
- **Limited Error Handling:** Error recovery in network failure scenarios is minimal.
- **No Load Balancing:** Does not support multiple server instances or load balancing.
- **No TLS Configuration:** Communication is not secured by default.

## Demo

In the project root dir, make sure the DUT Client, DUT agent and DUT Server are built:
```
go build ./cmds/dutctl
go build ./cmds/dutagent
go build ./cmds/exp/dutserver
```
Open up four terminal sessions referred to as `T1`, `T2`, `T3`, `T4`,

### Starting the Server
In `T1` start the DUT Server with default settings:

```bash
# Start the DUT Server locally on default port (1024)
./dutserver
```

### Registering Devices

Start a first agent in `T3` using a basic example configuration with one device and set the optional `-server` flag to register with the running DUT Server:

```bash
# Configure the agent to connect to the server
./dutagent -a localhost:1025 -c ./cmds/exp/contrib/config-1.yaml -server localhost:1024

```

In `T4` start a second agent with different port and another basic example configuration with another device:

```bash
# Configure the agent to connect to the server
./dutagent -a localhost:1026 -c ./cmds/exp/contrib/config-2.yaml -server localhost:1024

```

### Connecting with a Client

Play around with the DUT Client in `T2`

```bash
# List available devices using the default address localhost:1024 which is the DUT server
./dutctl list

# List the devices of a dedicated agent only
./dutctl -s localhost:1025 list

# Run a interactive command on a remote device via the DUT server
./dutctl device2 repeat
```

The complete functionality of the client is available via the DUT Server, see `./dutctl -h`

