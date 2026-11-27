# Let a command take arguments

A command hands arguments to its modules in one of three ways: fixed in the configuration, passed through from the
user to one module, or declared by the command and placed into the modules' arguments. This guide shows how to
configure each in the agent's configuration file. The [configuration reference](./dutagent-config.md#command-arguments)
lists the rules.

## Fix the arguments

If the user passes nothing, give each module its arguments with `args`:

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

Users run:

```bash
dutctl my-device power-cycle
```

## Pass the user's arguments through to one module

If the command takes exactly the arguments of one module, in that module's syntax, mark the module as the
passthrough module. Its help becomes the command's help.

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

Users run:

```bash
dutctl my-device run-command "ls -la /tmp"
```

The module receives `["ls -la /tmp"]`. The shell module expects the whole command as one argument, so users quote it.

## Name the arguments and place them

If the command takes arguments of its own and hands them to several modules, declare them under the command's `args`,
and put `${name}` where a module needs a value:

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

Users pass the values in the order of the declaration:

```bash
dutctl device flash-firmware fw.bin /backup/old.bin
```

> **Note:** Arguments are matched by position, not by name. Reordering the `args` declarations changes which value
> each template receives.

A template may be part of a longer argument, such as `/configs/${name}.yaml`, and stand beside fixed values, such as
`["${file}", "static", "${other}"]`.

## Examples

### Flash with verification

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

### Pulse a GPIO line

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
        label: Pulse line
      args: ["${duration}"]
```

```bash
dutctl device gpio-pulse 100ms
```
