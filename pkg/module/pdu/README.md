# PDU

The _PDU_ module provides basic power control of a Power Distribution Unit (PDU) via HTTP requests. It supports turning a power outlet on, off, toggling its state, and querying the current status.

This module supports Intellinet-style PDUs (e.g. Intellinet 163682, LogiLink PDU8P01), Gude and NETIO PDUs. Select the device with the `vendor` option.

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

| Option     | Type   | Description                                                    |
| ---------- | ------ | ------------------------------------------------------------- |
| `vendor`   | string | PDU HTTP API: `intellinet` (default), `gude` or `netio`               |
| `host`     | string | Base URL of the PDU, including scheme (e.g. `http://10.0.0.5`)        |
| `user`     | string | (Optional) Username for HTTP Basic Auth; set together with `password` |
| `password` | string | (Optional) Password for HTTP Basic Auth; set together with `user`    |
| `outlet`   | int    | Outlet to control (0-based, defaults to 0); upper bound is device-specific |

## NETIO

Set `vendor: netio` and `host` to the device's base URL, including its port if
needed, e.g. `http://192.168.1.200:8080`. Enable the JSON API in the NETIO web
interface and configure `user`/`password` with read-write access for power
commands. Status only needs read access.

`outlet: 0` selects NETIO output ID 1, `outlet: 1` selects ID 2, and so on.
Outputs are matched by ID, independently of their order in the response.
Status uses `GET /netio.json`; on/off/toggle use `POST /netio.json` with actions
1/0/4 and report the returned `State`. The NETIO short-off/reset action is not
exposed; a power-cycle command can compose `off`, `time-wait`, and `on`.

Protocol: [NETIO JSON API 2.6](https://www.netio-products.com/files/M2M-HTTP-JSON_NETIO-API-protocol_v2-6_.pdf).
