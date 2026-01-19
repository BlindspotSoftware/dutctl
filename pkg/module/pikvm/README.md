# PiKVM

This module provides comprehensive control of a DUT via a PiKVM device. It offers power management through ATX control, keyboard input simulation, and virtual media mounting capabilities.

PiKVM documentation: https://docs.pikvm.org/

Compatibility: This module uses PiKVM's HTTP API endpoints (e.g. `/api/atx/*`, `/api/hid/*`, `/api/msd/*`, `/api/streamer/snapshot`). It is intended for PiKVM (kvmd/PiKVM OS) systems, and should work with other devices only if they expose a compatible API.

## Features

### Power Management
Control the DUT's power state via ATX power and reset buttons:

- Power commands use PiKVM's ATX control API:
  - `on` is idempotent (does nothing if already powered on)
  - `off` performs a graceful shutdown via power button press
  - `force-off` performs a hard power off via long press (5+ seconds)
  - `reset` triggers an ATX reset button press
  - `force-reset` triggers a hardware hot reset

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

See [pikvm-key-strings.md](./pikvm-key-strings.md) for the full list of supported key strings.

```
COMMANDS:
  type <text>          Type a text string
  key <keyname>        Send a single key (e.g., Enter, Escape, F12)
  key-combo <keys>     Send key combination (e.g., Ctrl+Alt+Delete)

FLAGS (must come before the action):
  --delay <duration>   Delay between key events for key-combo (default: 500ms)
```

### Virtual Media
Mount ISO images or disk images as virtual USB devices:

- `mount` uploads images to PiKVM's storage (with automatic space management)
- `mount-url` instructs PiKVM to download the image directly from the URL
- Old images are automatically deleted when storage space is needed
- SHA256 hashing prevents duplicate uploads

```
COMMANDS:
  mount <path>         Mount an image file from the agent's filesystem
  mount-url <url>      Mount an image from a URL
  unmount              Unmount current virtual media
  media-status         Show mounted media information
```

### Screenshot
Capture the current display output from PiKVM's video stream.

## Configuration Options

| Option   | Type   | Default | Description                                                                                                                                          |
| -------- | ------ | ------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| host     | string | -       | Address of the PiKVM device (e.g., "192.168.1.100") Defaults to HTTPS if no scheme is specified, HTTP can be used by explicitly specifying `http://` |
| user     | string | admin   | Username for authentication                                                                                                                          |
| password | string | -       | Password for authentication                                                                                                                          |
| timeout  | string | 10s     | Timeout for HTTP requests (e.g., "10s", "30s")                                                                                                       |
| mode     | string | -       | **Required**: Mode ("power", "keyboard", "media", "screenshot")                                                                                      |

⚠️ **Security Warning**: Passwords are stored in plaintext in the configuration file. This should only be used in trusted environments.

## Usage Examples

See [pikvm-example-cfg.yml](./pikvm-example-cfg.yml) for comprehensive configuration examples.

### Basic Power Control
See [pikvm-example-power.yml](./pikvm-example-power.yml).

### Boot Menu Access
See [pikvm-example-keyboard.yml](./pikvm-example-keyboard.yml).

### ISO Mounting
See [pikvm-example-media.yml](./pikvm-example-media.yml).

### Screenshot Capture
See [pikvm-example-screenshot.yml](./pikvm-example-screenshot.yml).

## Requirements

- PiKVM device with API access enabled
- Network connectivity between dutagent and PiKVM
- Valid authentication credentials
