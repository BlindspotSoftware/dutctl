The _dummy_ package is a collection of the following demonstration modules:

- [Status](#Status)
- [Repeat](#Repeat)
- [File Transfer](#File-Transfer)

# Status

This module prints status information about itself and the environment.
It demonstrates the use of the Print method of module.Session to send messages to the client.

```
ARGUMENTS:
	[args...]

The module accepts any number of arguments and prints them back to the client.
```

See [dummy-example-cfg.yml](./dummy-example-cfg.yml) for examples.

## Configuration Options

_none_

# Repeat

This module repeats the input from the client.
It demonstrates the use of the Console method of module.Session to interact with the client
via stdin, stdout and stderr.

The module reads input line by line and echoes back single words. If more than one word is
provided on a line, the module terminates.

See [dummy-example-cfg.yml](./dummy-example-cfg.yml) for examples.

## Configuration Options

_none_

# File Transfer

This module demonstrates file transfer between client and dutagent.
It requests a file from the client, appends a marker string, and sends the processed file back.
It demonstrates the use of the RequestFile and SendFile methods of module.Session.

```
ARGUMENTS:
	<input-file> <output-file>

The module requires exactly two arguments:
  - input-file:  The name of the file to request from the client.
  - output-file: The name under which the processed file is sent back to the client.
```

See [dummy-example-cfg.yml](./dummy-example-cfg.yml) for examples.

## Configuration Options

_none_
