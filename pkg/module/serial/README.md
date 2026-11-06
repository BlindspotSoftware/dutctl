The serial package provides a single module:

# Serial

This module connects to the DUT's serial port from the _dutagent_, forwards the
serial output it reads to the _dutctl_ client, and (optionally) drives a
scripted `send`/`expect` sequence against the port, or bridges the port to your
terminal as an interactive console.

In monitor and scripted mode all input is supplied up front as arguments, which
makes the module suitable for scripts and automated callers. With **no step
arguments** it runs in **monitor** mode: it streams the serial output until the
session is cancelled (or `-t` elapses). With `-i` it runs in **interactive**
mode: see [Interactive mode](#interactive-mode).

```
ARGUMENTS:
	[-reconnect] [-t <duration>] [-eol cr|lf|crlf|none] [-keep-escapes]                 (monitor)
	[-reconnect] [-t <duration>] [-eol cr|lf|crlf|none] [-keep-escapes] [--] <step>...  (step sequence)
	[-reconnect] [-t <duration>] -i                                                (interactive)

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

While the monitor runs, the device is busy for other users, but your own other
commands may run beside it: a power cycle from a second terminal shows up in the
monitor's output. Start it once the monitor prints `--- Connected to … ---`, or
the first lines of the boot are lost. If the serial adapter disappears during a
power cycle, the monitor ends unless `-reconnect` is given.

A serial port serves one command at a time, unless dutagent runs as root.
Running the same command again is refused, and another command that opens the
port in use fails when it opens it, after the steps before it have run. Steps that must happen in a fixed order,
such as a power cycle followed by `serial` with `expect` steps, belong in one
command.

## Interactive mode

Invoking `serial -i` connects your terminal to the DUT's serial port, the way a
terminal program such as picocom or screen does. The bytes pass exactly as they
are on the wire in both directions, and nothing is filtered or matched:

- What the DUT sends reaches your terminal unchanged, escape sequences included,
  and your terminal interprets them. Screen-oriented programs such as `vi` or
  `top` work.
- What you type reaches the DUT unchanged. The client puts your terminal into
  raw mode, so Enter sends CR, which is what a serial console expects, and
  Ctrl-C reaches the DUT instead of interrupting the client.
- Ctrl-A is the client's escape key: **Ctrl-A x** ends the session, **Ctrl-A e**
  toggles a local echo for DUTs that do not echo what you type, and Ctrl-A
  pressed twice sends one Ctrl-A to the DUT.
- The `--- Connected ... ---` and `--- Connection closed ---` markers go to the
  client's stderr, so `2>/dev/null` hides them and `> capture.bin` holds the
  DUT's bytes and nothing else.
- Input from a pipe is forwarded as it arrives, with no raw mode anywhere.
  When it ends, the session keeps showing the DUT's output for a moment so the
  reply to the last input is visible, then ends by itself:
  `printf 'ls\r' | dutctl dev serial -i`.
- `-t` bounds the whole run; reaching it is a success, as in monitor mode.

`-i` takes no steps, and no `-eol` or `-keep-escapes`: those shape what a
scripted `send` appends and what the output filter strips, while the bridge
passes bytes verbatim. Giving them with `-i` is a usage error. The device's
`imap` [character maps](#character-maps) apply in every mode, the interactive
one included.

Like the monitor, an interactive session keeps the device busy for other users
until it ends, and your own other commands, a power cycle for instance, may run
beside it.

## Temporary disconnections

Pass `-reconnect` in any mode to wait for an initially absent device and reopen
it after a disconnect. Configure a stable path such as
`/dev/serial/by-id/usb-...`, so a replacement `/dev/ttyUSBn` number cannot select
another adapter. Reopening uses the same path and baud rate, at 250 ms intervals.
Permission, busy and configuration errors remain fatal. Without the flag, opening
or reading an unavailable device still fails immediately.

The session and device reservation stay open. Notices appear once per transition:

```text
--- Serial device disconnected: /dev/serial/by-id/usb-... ---
--- Waiting for serial device to reconnect: /dev/serial/by-id/usb-... ---
--- Serial device reconnected: /dev/serial/by-id/usb-... at 115200 baud ---
```

An initially absent device prints the waiting notice. Interactive notices use
stderr with CR LF; stdout continues to contain only DUT bytes. Input received
while disconnected is discarded, with a notice, rather than replayed after
reconnection. An interrupted interactive write is not replayed either.

Monitor output resumes. A pending `expect` resumes with its match buffer and
partial character-map/escape state cleared, so a match cannot span two boots.
Completed steps are never repeated. A send that has not started may wait for the
device; any failed attempted send terminates the script with uncertain delivery,
including short writes that failed partway through. It is never retried.

The original `-t` deadline includes all waiting and the final drain, and never
restarts on reconnection. Reaching it still succeeds in monitor/interactive mode;
an unfinished scripted step fails. With `-t 0`, waiting ends on cancellation (or
interactive input EOF). Ctrl-A x remains available while waiting in a console.
Newly opened ports are not flushed, preserving queued boot bytes; output emitted
while the adapter was absent cannot be recovered. Without `-reconnect`, the
initial input buffer is still flushed as before.

## Flags

| Flag | Description |
|------|-------------|
| `-t <duration>` | Global timeout for the whole run (e.g. `30s`, `3m`). `0` (default) means no timeout. |
| `-eol cr\|lf\|crlf\|none` | Line ending appended by `send`. Default `cr` (`\r`), which is what serial consoles expect on Enter. |
| `-keep-escapes` | Keep terminal escape sequences in the output instead of stripping them (e.g. for binary data or exact-byte capture). |
| `-i` | Interactive console: bridge the port to your terminal. Takes no steps, and no `-eol` or `-keep-escapes`. |
| `-reconnect` | Wait for an absent device and reopen after disconnection. Default off; see [Temporary disconnections](#temporary-disconnections). |

The `--` separator before the steps is optional; it is only needed if a step
value would otherwise look like a flag.

## Output and matching notes

- Serial output is forwarded to the client whenever the module is reading the
  port: in monitor mode, while an `expect` is waiting, and (if the last step is
  a `send`) for a moment afterwards so its reply is visible. Between sends with
  no following `expect` nothing is read, so nothing is forwarded. Step progress
  is reported with `--- [n/total] matched/sent … ---` markers.
- Terminal escape sequences (cursor moves, colour, queries) are stripped from
  the output before it is shown or matched, so they don't interfere with
  patterns. Pass `-keep-escapes` to keep them (for binary data or exact-byte
  capture).
- The device's `imap` [character maps](#character-maps), if any, are applied
  before the output is shown or matched: with `lfcrlf`, a pattern that spans
  lines sees `\r\n`.
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

# Interactive console (end it with Ctrl-A x).
serial -i

# Keep the same console through USB serial disconnections during power cycles.
serial -reconnect -i

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
| imap   | list   | Character maps applied to the device's output, see below (default: none) |

### Character maps

`imap` names substitutions for single bytes the device sends, applied in every
mode before the output is shown or matched. The names are picocom's (`--imap`);
tio, minicom, PuTTY and Tera Term offer the same substitutions under names of
their own.

| Name     | Effect                                   |
|----------|------------------------------------------|
| `lfcrlf` | a LF that no CR precedes becomes CR LF   |
| `crcrlf` | a CR that no LF follows becomes CR LF    |
| `crlf`   | CR becomes LF                            |
| `lfcr`   | LF becomes CR                            |
| `igncr`  | CR is dropped                            |
| `ignlf`  | LF is dropped                            |
| `delbs`  | DEL becomes BS                           |
| `bsdel`  | BS becomes DEL                           |

Most devices need none: the Linux console, U-Boot and a getty end their lines
in CR LF, which a raw terminal shows right as it is. A device that ends its
lines in a bare LF stair-steps on the raw terminal of an interactive session,
since the terminal does not return the cursor to the start of the line by
itself; `imap: [lfcrlf]` fixes that. Unlike picocom's, the two maps that add a
byte leave an existing CR LF pair alone, also one split across reads, so a
capture of the output stays byte-exact for such a device; `crcrlf` holds a
CR at the end of a read back until the next byte or a pause tells whether a
LF follows. What you type is never mapped: Enter sends CR.

[RE2]: https://golang.org/s/re2syntax
