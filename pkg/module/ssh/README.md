The _ssh_ package provides a single module:

# SSH

This module establishes an SSH connection to the DUT from the dutagent and executes a command.

```
ARGUMENTS:
    [command-string]

The connection is closed after the passed command is executed.
The command-string is passed to the shell as a single argument. The command-string must not contain any newlines.
Quote the command-string if it contains spaces or special characters. E.g.: "ls -l /tmp"
```

The module supports password and key-pair authentication. At least one needs to be [configured](#configuration-options). If both are configured, key-pair authentication is preferred and password authentication is used as fallback.

Optionally, the public key of the server to connect to (intentionally the DUT) can be set to be used during SSH handshake. If unset, any host key will be accepted.

See [ssh-example-cfg.yml](./ssh-example-cfg.yml) for examples. 

## Configuration Options

| Option | Value | Description
|----------|--------|------------------------------------|
| host | string | Hostname or IP address of the DUT |
| port | int | Port number of the SSH server on the DUT (default: 22) |
| user | string | Username for the SSH connection (default: "root") |
| password | string | Password for the SSH connection |
| privatekey | string | Path to the dutagent's private key file |
| hostkey | string | Server's host key in the format [key_type] [base64_encoded_key] |

