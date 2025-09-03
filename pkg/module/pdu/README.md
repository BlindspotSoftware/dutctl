# PDU

The _PDU_ module provides basic power control of a Power Distribution Unit (PDU) via HTTP requests. It supports turning a power outlet on, off, toggling its state, and querying the current status.

**Note**: This module currently supports only Intellinet ATM PDUs.

This module is intended to be used as part of `dutagent`, allowing automated power control of a DUT (Device Under Test) through a network-accessible PDU.

## Usage

```
pdu [on|off|toggle|status]
```

### Commands

| Command  | Description                    |
| -------- | ------------------------------ |
| `on`     | Power on the outlet            |
| `off`    | Power off the outlet           |
| `toggle` | Toggle the current power state |
| `status` | Report the current power state |

If no command is provided, the module prints a usage message and exits.

See [pdu-example-cfg.yml](./pdu-example-cfg.yml) for examples. 

## Configuration Options

| Option     | Type   | Description                                    |
| ---------- | ------ | ---------------------------------------------- |
| `host`     | string | Base URL of the PDU (e.g. `10.0.0.5`)          |
| `user`     | string | (Optional) Username for HTTP Basic Auth        |
| `password` | string | (Optional) Password for HTTP Basic Auth        |
| `outlet`   | int    | Outlet number to control (0-15, defaults to 0) |
