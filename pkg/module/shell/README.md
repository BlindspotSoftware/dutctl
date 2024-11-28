The _shell_ package provides a single module:

# Shell

This module executes a shell command on the DUT agent.

```
ARGUMENTS:
	[command-string]

The shell is executed with the -c flag and the the first argument to the module is passed as the command-string.
So make sure to quote the command-string if it contains spaces or special characters. E.g.: "ls -l /tmp"
The shell module is non-interactive and does not support stdin.
```

See [shell-example-cfg.yml](./shell-example-cfg.yml) for examples. 

## Configuration Options

| Option | Value | Description
|----------|--------|------------------------------------|
| path | string | Path is th path to the shell executable on the dutagent. Defaults to the system default shell  |
| quiet | bool | Suppress forwarding stout, but not sterr |