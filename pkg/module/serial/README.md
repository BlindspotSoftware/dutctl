The serial package provides a single module:

# Serial

This module connects to the DUT's serial port from the _dutagent_ and forwards
the serial output it reads to the _dutctl_ client. It runs in one of three modes,
selected by its arguments:

- **Monitor** (no arguments): stream the serial output until the session is
  cancelled (or `-t` elapses).
- **Interactive** (`-i`): a live console. Keystrokes go to the port and its
  output comes back, until you quit with Ctrl-A then x (or `-t` elapses).
- **Step sequence** (one or more steps): run a scripted `send`/`expect`
  sequence, supplied up front as arguments, which suits scripts and automated
  callers.

```
ARGUMENTS:
	[-t <duration>] [-eol cr|lf|crlf|none] [-keep-escapes]                 (monitor: stream output)
	[-t <duration>] [-keep-escapes] -i                                     (interactive console)
	[-t <duration>] [-eol cr|lf|crlf|none] [-keep-escapes] [--] <step>...  (run a step sequence)

	step := expect <regex> | send <data> | send-raw <data>
```

## Steps

Steps run in order, top to bottom. The run exits with success once all steps
complete, and with failure on the first `expect` that times out (or on a serial
error).

| Step | Behaviour |
|------|-----------|
| `expect <re2>` | Wait until the serial output matches the [RE2] regular expression. A plain string is a valid regex that matches itself; escape regex metacharacters (`. $ * + ? ( ) [ ] { } ^ \| \`) to match them literally, e.g. `expect '192\.168\.0\.1'`. |
| `send <data>` | Write `<data>` followed by the configured line ending (see `-eol`) to the port. |
| `send-raw <data>` | Write `<data>` verbatim, with no line ending appended. |

`send`/`send-raw` data supports the escapes `\r` `\n` `\t` `\\` and `\xNN`
(e.g. `\x03` for Ctrl-C). Each step value is a single argument, so quote values
that contain spaces.

If the last step is a `send`, the module keeps showing output for a moment
afterwards so the DUT's reply to that final input is visible.

## Monitor mode

Invoking `serial` with **no step arguments** streams the serial output to the
client until the session is cancelled (Ctrl-C / disconnect), or until `-t`
elapses — reaching the `-t` deadline in monitor mode is a success. No matching
is done.

## Interactive mode

`serial -i` bridges your terminal to the serial port. On a terminal, _dutctl_
switches it to raw mode, so every key goes to the DUT as typed: Ctrl-C, Ctrl-D,
Ctrl-Z and Ctrl-S/Ctrl-Q included. Quit with **Ctrl-A then x**; Ctrl-A twice
sends a single Ctrl-A. The terminal is restored when _dutctl_ exits (except
after `kill -9`; run `reset` then). With piped input there is no raw mode and
the input is forwarded as is. `-i` cannot be combined with steps.

## Reconnect

If the serial device disappears mid-session (e.g. an FTDI chip that powers
down with the DUT, or a pulled USB cable), the module waits for it to come back
and carries on, in every mode:

```
--- Serial device disconnected, waiting to reconnect ---
--- Serial device reconnected ---
```

Both events are also written to the _dutagent_ log. Keys typed while the device
is gone are discarded, not replayed later, and the console says so once. The
`-t` deadline still applies while waiting.

## Flags

| Flag | Description |
|------|-------------|
| `-t <duration>` | Global timeout for the whole run (e.g. `30s`, `3m`). `0` (default) means no timeout. |
| `-eol cr\|lf\|crlf\|none` | Line ending appended by `send`. Default `cr` (`\r`), which is what serial consoles expect on Enter. |
| `-keep-escapes` | Keep all terminal escape sequences in the output instead of removing them (e.g. for binary data or exact-byte capture). |
| `-i` | Interactive console (see above). Cannot be combined with steps. |

The `--` separator before the steps is optional; it is only needed if a step
value would otherwise look like a flag.

## Output and matching notes

- Serial output is forwarded to the client whenever the module is reading the
  port: in monitor mode, while an `expect` is waiting, and (if the last step is
  a `send`) for a moment afterwards so its reply is visible. Between sends with
  no following `expect` nothing is read, so nothing is forwarded. Step progress
  is reported with `--- [n/total] matched/sent … ---` markers.
- Monitor and step modes remove terminal escape sequences (cursor moves,
  colour, queries) from the output before it is shown or matched, so they don't
  interfere with patterns.
- Interactive mode keeps the sequences that draw the screen (cursor moves,
  colour, erase, scrolling), so `vi`, `top` or `less` work. It also keeps
  queries that your terminal answers with plain numbers, such as the cursor
  position (`ESC[6n`) that `resize` and `vim` ask for; the answer goes to the
  DUT as input, as on any serial terminal. It removes what acts on your
  terminal itself or would make it answer with text the DUT chose: window
  titles, clipboard writes, string queries (OSC, DCS) and window operations,
  including the title report (`ESC[21t`).
- `-keep-escapes` keeps everything, in every mode (for binary data or
  exact-byte capture).
- Expect matching uses a rolling window of the most recent **64 KiB** of
  output, so a single pattern cannot span more than that.
- Match on distinctive markers/prompts rather than `^`/`$` anchors — the rolling
  buffer has no stable line-start or end-of-text.
- The DUT may echo what you send, so an `expect` immediately after a `send` can
  match your own input rather than the device's reply.

The syntax of the regular expressions accepted is the syntax accepted by RE2 and
described at https://golang.org/s/re2syntax.

## Examples

```
# Monitor the console (stream until cancelled).
serial

# Interactive console; quit with Ctrl-A then x.
serial -i

# Wait for a boot marker, then succeed.
serial -- expect 'Welcome to'

# Log in and run a command.
serial -t 60s -- expect 'login:' send root expect 'Password:' send secret expect '# ' send reboot

# Send Ctrl-C, then expect prompt.
serial -- send-raw '\x03' expect '=>'

# Reboot, then see the output that follows the final send.
serial -- expect '# ' send reboot
```

See [serial-example-cfg.yml](./serial-example-cfg.yml) for a configuration
example.

## Configuration Options

| Option | Value  | Description                                                         |
|--------|--------|---------------------------------------------------------------------|
| port   | string | Path to the serial device on the dutagent (e.g. `/dev/ttyUSB0`)     |
| baud   | int    | Baud rate of the serial connection (default: 115200)                |
| delay  | string | Pause before each send, e.g. `200ms` (default: 50ms; `0s` disables) |

[RE2]: https://golang.org/s/re2syntax
