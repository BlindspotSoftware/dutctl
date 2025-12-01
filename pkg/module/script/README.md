# Script Module

The _script_ module executes a configured script file on the DUT Agent.

## Overview

This module executes a script file specified in the configuration. All arguments passed at runtime are forwarded to the script as positional parameters.

The script's working directory is set to the directory containing the script file. All runtime arguments are passed to the script in order as positional parameters ($1, $2, etc.).

### Exit Code Handling

The module returns an error if the script exits with a non-zero exit code. Script execution failures will cause the command to fail.

### Output Handling

- **stdout**: Printed to the client unless `quiet` mode is enabled
- **stderr**: Always printed regardless of quiet mode

### Arguments

All arguments passed at runtime are forwarded to the script as positional parameters.

## Configuration Options

| Option      | Type   | Required | Description                                                                                          |
|-------------|--------|----------|------------------------------------------------------------------------------------------------------|
| path        | string | Yes      | Absolute path to the script file on the dutagent                                                     |
| interpreter | string | No       | Path to interpreter (e.g., `/bin/bash`). If not set, script must have execute permission (+x)       |
| quiet       | bool   | No       | Suppress stdout output. Stderr is always printed regardless of this setting                          |

## Examples

### Basic Script Execution

```yaml
power:
  desc: "Control device power"
  modules:
    - module: script
      main: true
      options:
        path: "/opt/scripts/power-control.sh"
        interpreter: "/bin/bash"
```

Usage:

```bash
# Pass "on" as first argument to script
dutctl device power "on"

# Pass "off" as first argument
dutctl device power "off"

# Pass multiple arguments
dutctl device power "reset" "--force"
```

### Python Script

```yaml
test-runner:
  desc: "Control device power"
  modules:
    - module: script
      main: true
      options:
        path: "/opt/tests/run_tests.py"
        interpreter: "/usr/bin/python3"
```

Usage:

```bash
dutctl device test-runner "unit" "--verbose"
```

### Executable Script (No Interpreter)

```yaml
power:
  desc: "Backup configuration"
  modules:
    - module: script
      main: true
      options:
        # No interpreter - script must have +x permission
        path: "/usr/local/bin/power-control.sh"
```

Usage:

```bash
dutctl device power "on"
```

The module is non-interactive and does not support stdin.
