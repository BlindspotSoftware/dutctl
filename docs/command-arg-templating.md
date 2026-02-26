# Command Arguments Guide

This guide explains how runtime arguments are distributed to modules in a dutctl command.

## Overview

Each module in a command receives its arguments in one of two ways:

- **Static args** — values fixed in the YAML config (`args: [...]`)
- **Runtime args** — values provided by the caller at invocation time

There are two mechanisms for delivering runtime args to modules: command-level templating and `forwardArgs`. They can be used independently or together.

---

## Static Args

Modules that do not need runtime input have their arguments fixed in the config:

```yaml
power-cycle:
  desc: "Power cycle the device"
  modules:
    - module: gpio-switch
      args: ["power-pin", "off"]
    - module: time-wait
      args: ["2000"]
    - module: gpio-switch
      args: ["power-pin", "on"]
```

```bash
dutctl run my-device power-cycle
# No runtime arguments needed.
```

---

## Command-Level Templating

Declare named arguments under `args:` and reference them in module args with `${name}` syntax. Arguments are matched positionally in declaration order.

```yaml
flash-firmware:
  desc: "Flash firmware to the device"
  args:
    - name: firmware-file
      desc: "Path to firmware binary"
    - name: backup-path
      desc: "Backup location"
  modules:
    - module: shell
      args: ["flashrom", "-r", "${backup-path}"]
    - module: file
      args: ["${firmware-file}", "/tmp/fw.bin"]
    - module: flash
      args: ["/tmp/fw.bin"]
```

```bash
dutctl run device flash-firmware fw.bin /backup/old.bin
# firmware-file = "fw.bin"
# backup-path   = "/backup/old.bin"
```

Templates can be embedded anywhere in a string: `"/configs/${name}.yaml"`, `"--device=${id}"`.

---

## forwardArgs

Mark one module with `forwardArgs: true` to receive all runtime arguments not consumed by command-level template substitution. If no `args:` are declared, all runtime arguments are forwarded.

At most one module per command may be marked `forwardArgs`.

```yaml
run-command:
  desc: "Run a shell command on the device"
  modules:
    - module: shell
      forwardArgs: true
```

```bash
dutctl run my-device run-command ls -la /tmp
# shell receives: ["ls", "-la", "/tmp"]
```

When combined with command-level templating, the first `N` runtime args (one per declared `args` entry) are consumed for template substitution. The remaining args are forwarded:

```yaml
flash-and-run:
  desc: "Flash firmware, then run a command on the device"
  args:
    - name: firmware-file
      desc: "Path to firmware binary"
  modules:
    - module: file
      args: ["${firmware-file}", "/tmp/fw.bin"]
    - module: flash
      args: ["/tmp/fw.bin"]
    - module: shell
      forwardArgs: true
```

```bash
dutctl run device flash-and-run firmware.bin verify --verbose
# firmware-file = "firmware.bin"  →  consumed by template substitution
# ["verify", "--verbose"]         →  forwarded to shell
```

---
