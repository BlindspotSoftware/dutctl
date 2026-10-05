The _dummy_ package is a collection of the following demonstration modules:

- [Status](#Status)
- [Console](#Console)
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

# Console

This module echoes the user's input through a console.
It demonstrates the use of the OpenConsole method of module.Session in both of its modes
and lets a user see what the client delivers to a module.

```
ARGUMENTS:
	[raw|hex|line]

The module runs one of three demos, selected by its only argument:
  - raw:  (default) opens a raw console and echoes every byte as it was received,
          control characters and escape sequences included. A lone Ctrl-D ends the demo.
  - hex:  opens a raw console and prints each chunk of input as one line of hex bytes,
          so you see exactly which bytes a key produces. A lone Ctrl-D ends the demo.
  - line: opens a line console and echoes each line as "> " followed by the line.
          The line "quit" or the end of input ends the demo.
```

The raw and hex demos are meant for a terminal, where the client puts it into raw mode so keys
reach the module as typed; with piped input they echo the chunks as read and end at the input's
end. The line demo also works with piped input, for example
`printf 'hello\nquit\n' | dutctl <device> console line`.

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
