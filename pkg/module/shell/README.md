The _shell_ package provides a single module:

# Shell

This module executes shell commands or scripts on the DUT Agent. It operates in two modes depending on the configured `path`:

## Command Mode

When `path` is a shell executable (sh, bash, zsh, dash, ksh, csh, tcsh, fish), the module executes shell commands:

```
ARGUMENTS:
	[command-string]

The shell is executed with the -c flag and the first argument to the module is passed as the command-string.
Quote the command-string if it contains spaces or special characters. E.g.: "ls -l /tmp"
```

Example configuration:
```yaml
test:
  modules:
    - module: shell
      options:
        path: "/bin/bash"
```

Example usage: `dutctl device test "ls -la"`

## Script Mode

When `path` is a script file, the module executes the script with runtime arguments:

```
ARGUMENTS:
	[arg1] [arg2] ...

The script is executed directly with all runtime arguments passed to it.
The script's working directory is set to the script's parent directory.
```

Example configuration:
```yaml
power:
  modules:
    - module: shell
      options:
        path: "/opt/scripts/power-control.sh"
```

Example usage: `dutctl device power "on"`

## Configuration Options

| Option | Value  | Description                                                                                   |
|--------|--------|-----------------------------------------------------------------------------------------------|
| path   | string | Path to the shell executable or script file. Defaults to /bin/sh if unset                     |
| quiet  | bool   | Suppress forwarding stdout, stderr will be forwarded regardless                                |

## Notes

- The shell module is non-interactive and does not support stdin
- In script mode, the script must be executable (or use a shell as the path)
- Script path is validated during module initialization

See [shell-example-cfg.yml](./shell-example-cfg.yml) for examples.
