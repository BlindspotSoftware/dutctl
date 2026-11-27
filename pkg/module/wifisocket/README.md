# WiFi socket (Tasmota)

The module `wifisocket` controls Tasmota-based WiFi sockets (for example NOUS A1T) via the device HTTP API (/cm endpoint).

**Note**: This module currently supports only Tasmota-based WiFi sockets.

This module is intended to be used as part of `dutagent`, allowing automated power control of a DUT (Device Under Test) through a network-accessible wifisocket.

## Arguments

```
wifisocket [on|off|toggle|status]
```


| Command  | Description                                   |
| -------- | --------------------------------------------- |
| `on`     | Power on the configured channel               |
| `off`    | Power off the configured channel              |
| `toggle` | Toggle the configured channel                 |
| `status` | Query and report the configured channel state |

If no command is provided, the module prints a usage message and exits.

## Configuration Options

| Option     | Type   | Description                                                                 |
| ---------- | ------ | --------------------------------------------------------------------------- |
| `host`     | string | Base URL or IP of the device (e.g. `http://192.168.1.50` or `192.168.1.50`) |
| `user`     | string | (Optional) HTTP Basic Auth username                                         |
| `password` | string | (Optional) HTTP Basic Auth password                                         |
| `channel`  | int    | Channel number to control (1 for single-outlet devices)                     |

## Example

See [wifisocket-example-cfg.yml](./wifisocket-example-cfg.yml).
