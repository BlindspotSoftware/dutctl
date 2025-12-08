# PiKVM

This module provides comprehensive control of a DUT via a PiKVM device. It offers power management through ATX control, keyboard input simulation, and virtual media mounting capabilities.

## Features

### Power Management
Control the DUT's power state via ATX power and reset buttons:

```
COMMANDS:
  on           Power on (does nothing if already on)
  off          Graceful shutdown (soft power-off)
  force-off    Force power off (hard shutdown, 5+ second press)
  reset        Reset via ATX reset button
  force-reset  Force reset (hardware hot reset)
  status       Query current power state
```

### Keyboard Control
Send keyboard input to the DUT:

```
COMMANDS:
  type <text>          Type a text string
  key <keyname>        Send a single key (e.g., Enter, Escape, F12)
  key-combo <keys>     Send key combination (e.g., Ctrl+Alt+Delete)
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

| Option   | Type   | Default | Description                                                          |
| -------- | ------ | ------- | -------------------------------------------------------------------- |
| host     | string | -       | Address of the PiKVM device (e.g., "192.168.1.100")                 |
| user     | string | admin   | Username for authentication                                          |
| password | string | -       | Password for authentication                                          |
| timeout  | string | 10s     | Timeout for HTTP requests (e.g., "10s", "30s")                      |
| command  | string | -       | **Required**: Command type ("power", "keyboard", "media", "screenshot") |

⚠️ **Security Warning**: Passwords are stored in plaintext in the configuration file. This should only be used in trusted environments.

## API Endpoints Used

This module interacts with the following PiKVM API endpoints:

- `/api/atx` - Get ATX power status
- `/api/atx/power` - Intelligent power management (on/off/off_hard/reset_hard)
- `/api/atx/click` - ATX button control (reset)
- `/api/hid/print` - Type text input
- `/api/hid/events/send_key` - Send keyboard keys and combinations
- `/api/msd` - Mass Storage Device (virtual media) status
- `/api/msd/write` - Upload images to PiKVM storage
- `/api/msd/write_remote` - Download images from URL to PiKVM
- `/api/msd/set_params` - Configure virtual media parameters
- `/api/msd/set_connected` - Mount/unmount media
- `/api/msd/remove` - Delete images from storage
- `/api/streamer/snapshot` - Capture screenshot

## Usage Examples

See [pikvm-example-cfg.yml](./pikvm-example-cfg.yml) for comprehensive configuration examples.

### Basic Power Control

```yaml
version: 0
devices:
  my-server:
    desc: "Server controlled via PiKVM"
    cmds:
      power:
        desc: "Power management: on|off|force-off|reset|force-reset|status"
        modules:
          - module: pikvm
            main: true
            options:
              host: https://pikvm.local
              user: admin
              password: admin
              command: power
```

### Boot Menu Access

```yaml
keyboard:
  desc: "Keyboard control: type <text>|key <keyname>|key-combo <combo>"
  modules:
    - module: pikvm
      main: true
      options:
        host: https://pikvm.local
        user: admin
        password: admin
        command: keyboard

# Usage: dutctl my-server keyboard key F12
```

### ISO Mounting

```yaml
media:
  desc: "Virtual media control: mount <path>|mount-url <url>|unmount|media-status"
  modules:
    - module: pikvm
      main: true
      options:
        host: https://pikvm.local
        user: admin
        password: admin
        command: media

# Usage: dutctl my-server media mount-url https://releases.ubuntu.com/22.04/ubuntu-22.04-live-server-amd64.iso
```

### Screenshot Capture

```yaml
screenshot:
  desc: "Capture a screenshot from PiKVM"
  modules:
    - module: pikvm
      main: true
      options:
        host: https://pikvm.local
        user: admin
        password: admin
        command: screenshot

# Usage: dutctl my-server screenshot
# The screenshot will be saved to the current directory
```

## Requirements

- PiKVM device with API access enabled
- Network connectivity between dutagent and PiKVM
- Valid authentication credentials

## Notes

- The module defaults to HTTPS if no scheme is specified in the host
- HTTP can be used by explicitly specifying `http://` in the host
- Power commands use intelligent API:
  - `on` - Does nothing if already powered on (idempotent)
  - `off` - Graceful shutdown via power button press
  - `force-off` - Hard power off via long press (5+ seconds)
  - `reset` - Reset button press
  - `force-reset` - Hardware hot reset
- Virtual media:
  - `mount` uploads images to PiKVM's storage (with automatic space management)
  - `mount-url` instructs PiKVM to download the image directly from the URL
  - Old images are automatically deleted when storage space is needed
  - SHA256 hashing prevents duplicate uploads
- Screenshot functionality captures the current display output from PiKVM's video stream
