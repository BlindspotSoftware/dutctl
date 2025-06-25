# IPMI

This module provides power management capabilities to the DUT via the Intelligent Platform Management Interface (IPMI). The dutagent forwards commands such as power on/off, reset, and status queries to the BMC of the DUT over the network.```

```
COMMANDS:
  on       Power on the device
  off      Power off the device
  cycle    Power cycle the device (off, then on)
  reset    Reset the device (hard reset, if supported)
  status   Show the current power status
```

The module connects to the BMC using a configurable host, port, user, and password.

⚠️ **Security Warning**: In this first implementation, the IPMI password is stored in plaintext in the configuration file. This poses a security risk and should only be used in trusted environments.

This module does not support reading stdin from the user or sending arbitrary IPMI commands. It is intended for basic chassis power control only.

See [ipmi-example-cfg.yml](./ipmi-example-cfg.yml) for examples.

## Configuration Options

| Option   | Value  | Description                                      |
| -------- | ------ | ------------------------------------------------ |
| host     | string | Hostname or IP address of the BMC                |
| port     | int    | Port number of the IPMI interface (default: 623) |
| user     | string | Username for IPMI authentication                 |
| password | string | Password for IPMI authentication                 |

⚠️ **Security Warning**: Passwords are stored in plaintext in the configuration file in this first implementation. Future versions will implement secure credential storage mechanisms.
