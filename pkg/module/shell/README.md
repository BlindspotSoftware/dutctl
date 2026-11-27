# Shell

The module `shell` executes a shell command on the DUT Agent.

## Arguments

```
ARGUMENTS:
	[command-string]

The shell is executed with the -c flag and the the first argument to the module is passed as the command-string.
So make sure to quote the command-string if it contains spaces or special characters. E.g.: "ls -l /tmp"
The shell module is non-interactive and does not support stdin.
```

## Configuration Options

| Option | Type   | Description                                                                                   |
|--------|--------|-----------------------------------------------------------------------------------------------|
| path   | string | Path to the shell executable on the dutagent. Defaults to the system default shell |
| quiet  | bool   | Suppress forwarding to stdout, but not stderr                                                 |

## Example

See [shell-example-cfg.yml](./shell-example-cfg.yml).
