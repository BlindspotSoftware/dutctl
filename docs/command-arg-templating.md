# Command Arguments Guide

This guide explains the three ways to handle arguments in dutctl commands: non-main, main, and templating.

## Three Mutually Exclusive Approaches

Commands support three approaches for arguments. **These approaches are mutually exclusive** - a command must use exactly one:

1. **Non-Main Commands** - Static values only, no runtime arguments
2. **Main Commands** - Single main module receives all runtime arguments
3. **Command-Level Templating** - Named arguments distributed to modules via templates

You cannot mix approaches (e.g., a command cannot have both an main module AND command-level args).

## Non-Main Commands

For commands without runtime arguments, you specify static values directly in module args:

```yaml
devices:
  my-device:
    cmds:
      power-cycle:
        desc: "Power cycle the device"
        uses:
          - module: gpio-switch
            args: ["power-pin", "off"]
          - module: time-wait
            args: ["2000"]
          - module: gpio-switch
            args: ["power-pin", "on"]
```

Usage:

```bash
dutctl run my-device power-cycle
```

No runtime arguments needed - all values are configured in the YAML.

## Main Commands

Commands with an main module pass all runtime arguments directly to that module:

```yaml
devices:
  my-device:
    cmds:
      run-command:
        desc: "Run shell command"
        uses:
          - module: shell
            main: true
```

Usage:

```bash
dutctl run my-device run-command ls -la /tmp
```

The main module receives: `["ls", "-la", "/tmp"]`

All runtime arguments go to the main module - you cannot have multiple main modules in one command.

## Command-Level Templating

Declare named arguments at the command level and distribute them to modules using `${arg-name}` template syntax. Arguments are mapped positionally in declaration order.

```yaml
flash-firmware:
  desc: "Flash firmware to device"
  args:
    - name: firmware-file
      desc: "Path to firmware binary"
    - name: backup-path
      desc: "Backup location"
  uses:
    - module: shell
      args: ["flashrom", "-r", "${backup-path}"]
    - module: file
      args: ["${firmware-file}", "/tmp/fw.bin"]
    - module: flash
      args: ["/tmp/fw.bin"]
```

Usage: `dutctl run device flash-firmware fw.bin /backup/old.bin`

Templates can be embedded in strings (`/configs/${name}.yaml`) and mixed with static values (`["${file}", "static", "${other}"]`).

## Examples

### Flash with Verification

```yaml
flash-verify:
  desc: "Flash firmware and verify"
  args:
    - name: firmware-path
      desc: "Path to firmware binary"
  uses:
    - module: file
      args: ["${firmware-path}", "/tmp/fw.bin"]
    - module: flash
      args: ["/tmp/fw.bin"]
    - module: time-wait
      args: ["500"]
    - module: shell
      args: ["flashrom", "-v", "/tmp/fw.bin"]
```

```bash
dutctl run device flash-verify /path/to/firmware.bin
```

### GPIO Control with Parameters

```yaml
gpio-pulse:
  desc: "Pulse GPIO pin"
  args:
    - name: pin-name
      desc: "GPIO pin identifier"
    - name: duration
      desc: "Pulse duration in milliseconds"
  uses:
    - module: gpio-switch
      args: ["${pin-name}", "on"]
    - module: time-wait
      args: ["${duration}"]
    - module: gpio-switch
      args: ["${pin-name}", "off"]
```

```bash
dutctl run device gpio-pulse reset-button 100
```
