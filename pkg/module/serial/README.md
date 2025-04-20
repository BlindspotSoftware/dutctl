The serial package provides a single module:

# Serial

This module establishes an serial connection to the DUT from the dutagent and  forwards the output.

```
ARGUMENTS:
	[-t <duration>] [<expect>]

If a regex is provided, the module will wait for the regex to match on the serial output, 
then exit with success.
If no expect string is provided, the module will read from the serial port until it is terminated by a signal (e.g. Ctrl-C).
The  expect string supports regular expressions. The optional -t flag specifies the maximum time to wait for the regex to match.
The regex is passed to the shell as a single argument. The regex must not contain any newlines.
Quote the regex if it contains spaces or special characters. E.g.: "(?i)hello\s+world!? dutctl"
```

The syntax of the regular expressions accepted is the syntax accepted by RE2 and described at https://golang.org/s/re2syntax.

The module is read-only at that time and does not support, sending bytes over the serial connection.

See [serial-example-cfg.yml](./serial-example-cfg.yml) for examples. 

## Configuration Options

| Option | Value | Description
|----------|--------|------------------------------------|
| port | string | Hostname or IP address of the DUT |
| baud | int | Port number of the SSH server on the DUT (default: 22) |


