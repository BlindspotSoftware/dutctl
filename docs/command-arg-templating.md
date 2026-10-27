# Command Arguments Guide

This guide explains the three ways to handle arguments in dutctl commands: static, passthrough, and templating.

## Three Mutually Exclusive Approaches

Commands support three approaches for arguments. **These approaches are mutually exclusive** - a command must use exactly one:

1. **Static Commands** - Static values only, no runtime arguments
2. **Passthrough Commands** - Single passthrough module receives all runtime arguments
3. **Command-Level Templating** - Named arguments distributed to modules via templates

You cannot mix approaches (e.g., a command cannot have both a passthrough module AND command-level args).

## Static Commands

For commands without runtime arguments, you specify static values directly in module args:

```yaml
devices:
  my-device:
    cmds:
      power-cycle:
        desc: "Power cycle the device"
        uses:
          - module: pdu
            with:
              host: http://10.0.0.5
              outlet: 1
            args: ["off"]
          - module: time-wait
            args: ["2s"]
          - module: pdu
            with:
              host: http://10.0.0.5
              outlet: 1
            args: ["on"]
```

Usage:

```bash
dutctl my-device power-cycle
```

No runtime arguments needed - all values are configured in the YAML.

## Passthrough Commands

Commands with a passthrough module pass all runtime arguments directly to that module:

```yaml
devices:
  my-device:
    cmds:
      run-command:
        desc: "Run shell command"
        uses:
          - module: shell
            passthrough: true
```

Usage:

```bash
dutctl my-device run-command "ls -la /tmp"
```

The passthrough module receives: `["ls -la /tmp"]`. The shell module expects the whole command as one argument, hence the quotes.

All runtime arguments go to the passthrough module - you cannot have multiple passthrough modules in one command.

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
    - module: flash
      with:
        tool: flashrom
        programmer: dediprog
      args: ["read", "${backup-path}"]
    - module: flash
      with:
        tool: flashrom
        programmer: dediprog
      args: ["write", "${firmware-file}"]
```

Usage: `dutctl device flash-firmware fw.bin /backup/old.bin`

Templates can be embedded in strings (`/configs/${name}.yaml`) and mixed with static values (`["${file}", "static", "${other}"]`).

> **Note:** Arguments are matched by position, not by name. The first runtime
> argument maps to the first declared arg, the second to the second, and so on.
> Reordering the `args` declarations changes which runtime value each template receives.

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
      with:
        operation: upload
        destination: /tmp/fw.bin
      args: ["${firmware-path}"]
    - module: shell
      args: ["flashrom -p dediprog -w /tmp/fw.bin"]
    - module: time-wait
      args: ["500ms"]
    - module: shell
      args: ["flashrom -p dediprog -v /tmp/fw.bin"]
```

```bash
dutctl device flash-verify /path/to/firmware.bin
```

### GPIO Control with Parameters

```yaml
gpio-pulse:
  desc: "Pulse GPIO pin"
  args:
    - name: duration
      desc: "Pulse duration, such as 100ms"
  uses:
    - module: gpio-button
      with:
        pin: 17
      args: ["${duration}"]
```

```bash
dutctl device gpio-pulse 100ms
```
