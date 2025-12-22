# PiKVM

This module provides comprehensive control of a DUT via a PiKVM device. It offers power management through ATX control, keyboard input simulation, and virtual media mounting capabilities.

## Features

### Power Management
Control the DUT's power state via ATX power and reset buttons:

```
COMMANDS:
  on           Power on via short ATX power button press
  off          Graceful shutdown via short ATX power button press
  force-off    Force power off via long ATX power button press (4-5 seconds)
  reset        Reset via short ATX reset button press
  force-reset  Force reset via long ATX reset button press
  status       Query current power state
```

### Keyboard Control
Send keyboard input to the DUT:

```
COMMANDS:
  type <text>          Type a text string
  key <keyname>        Send a single key (e.g., Enter, Escape, F12)
  combo <keys>         Send key combination (e.g., Ctrl+Alt+Delete)
  paste                Paste text from stdin
```

### Virtual Media
Mount ISO images or disk images as virtual USB devices:

```
COMMANDS:
  mount <path>         Mount an image file from the agent's filesystem
  mount-url <url>      Mount an image from a URL
  unmount              Unmount current virtual media
  media-status         Show mounted media information
```

## Configuration Options

| Option   | Type   | Default | Description                                              |
| -------- | ------ | ------- | -------------------------------------------------------- |
| host     | string | -       | Address of the PiKVM device (e.g., "192.168.1.100")     |
| user     | string | admin   | Username for authentication                              |
| password | string | -       | Password for authentication                              |
| timeout  | string | 10s     | Timeout for HTTP requests (e.g., "10s", "30s")          |

⚠️ **Security Warning**: Passwords are stored in plaintext in the configuration file. This should only be used in trusted environments.

## API Endpoints Used

This module interacts with the following PiKVM API endpoints:

- `/api/atx/power` - Power management status
- `/api/atx/click` - ATX button control (power/reset)
- `/api/hid/events/send_key` - Keyboard input simulation
- `/api/msd` - Mass Storage Device (virtual media) control
- `/api/msd/write` - Upload images to PiKVM
- `/api/msd/set_connected` - Mount/unmount media

## Usage Examples

See [pikvm-example-cfg.yml](./pikvm-example-cfg.yml) for comprehensive configuration examples.

### Basic Power Control

```yaml
version: 0
devices:
  my-server:
    desc: "Server controlled via PiKVM"
    cmds:
      power-on:
        desc: "Power on the server"
        modules:
          - module: pikvm
            main: true
            args:
              - on
            options:
              host: https://pikvm.local
              user: admin
              password: admin
```

### Boot Menu Access

```yaml
enter-bios:
  desc: "Press F12 to enter boot menu"
  modules:
    - module: pikvm
      main: true
      args:
        - key
        - F12
      options:
        host: https://pikvm.local
        user: admin
        password: admin
```

### ISO Mounting

```yaml
mount-installer:
  desc: "Mount Ubuntu installer ISO"
  modules:
    - module: pikvm
      main: true
      args:
        - mount-url
        - https://releases.ubuntu.com/22.04/ubuntu-22.04-live-server-amd64.iso
      options:
        host: https://pikvm.local
        user: admin
        password: admin
```

## Requirements

- PiKVM device with API access enabled
- Network connectivity between dutagent and PiKVM
- Valid authentication credentials

## Notes

- The module defaults to HTTPS if no scheme is specified in the host
- HTTP can be used by explicitly specifying `http://` in the host
- Long button presses (force-off, force-reset) hold the button for 4-5 seconds
- Virtual media images are uploaded to PiKVM's storage when using the `mount` command
- The `mount-url` command instructs PiKVM to download the image directly from the URL
