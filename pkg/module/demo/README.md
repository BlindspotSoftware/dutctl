The _demo_ package provides modules that need no hardware, so that you can try DUT Control on your laptop and learn
how to write a module. They come in two kinds:

- The **wrap modules** each wrap one feature of [module.Session](../module.go) and do nothing else. They show what a
  module can do, one feature at a time.
  - [Print](#print) (`wrap-print`)
  - [Prompt](#prompt) (`wrap-prompt`)
  - [Raw](#raw) (`wrap-raw`)
  - [Upload](#upload) (`wrap-upload`)
  - [Download](#download) (`wrap-download`)
  - [Fault](#fault) (`wrap-fault`)
- The **demo modules** drive a simulated board, each one part of it, as real modules drive the parts of the wiring to
  a real device. They show how to build a module for a device, and how a configuration turns such modules into
  commands.
  - [Switch](#switch) (`demo-switch`)
  - [Console](#console) (`demo-console`)
  - [Flash](#flash) (`demo-flash`)

The board has a power line, a flash that holds its firmware and has a write-protect line, and a serial console with a
tiny shell. Its simulation
lives in [internal/board](./internal/board) and stands for the hardware and its drivers: you need not read it to
understand the modules. All demo modules of an agent share one board, as the commands of a real device share its
hardware: a power-on by one command boots the board on the console that another command has open. Since the board
exists only in the agent's memory, it is off and holds its factory firmware whenever the agent starts.

## Try it

The [example configuration](./demo-example-cfg.yml) sets up two devices. Start an agent with it from the repository's
root, and run the commands of the device `agent` in a second terminal. Each runs one wrap module:

```
dutagent -c pkg/module/demo/demo-example-cfg.yml

dutctl agent echo hello world
dutctl agent hello Ada
dutctl agent prompt
dutctl agent keys
dutctl agent upload README.md
dutctl agent download note.txt
dutctl agent fail
dutctl agent crash
```

The device `demo-board` is the simulated board. Attach to its console, which stays silent while the board is off:

```
dutctl demo-board console
```

In a third terminal, switch the board on and watch it boot in the second one; type `help` there for the shell's
commands, and `exit` or Ctrl-A x to end the session. Then write new firmware, any file will do, and see the board boot
it:

```
dutctl demo-board power on
dutctl demo-board update README.md
```

Protect the flash, and the next update stops at the flash step, which leaves the board off, until you lift the
protection:

```
dutctl demo-board protect
dutctl demo-board update README.md
dutctl demo-board unprotect
```

`reboot` power-cycles the board and shows the boot on a console of its own, which ends once the board is up. End the
console in the second terminal first, since what one console shows of the board's output, another never sees:

```
dutctl demo-board reboot
```

## Modules and commands

A module provides a function; a command gives it a meaning on its device. The demo modules know nothing of what the
board is for: `demo-switch` switches the line that the option `line` wires it to, `demo-flash` writes and reads a
flash. The configuration of `demo-board` makes commands of them:

- `power` wires `demo-switch` to the power line and passes the user's arguments through, so the module's own syntax,
  `on`, `off`, `toggle`, `cycle` and `status`, is the command's.
- `protect` and `unprotect` wire `demo-switch` to the write-protect line and fix its argument, `on` and `off`. The
  module's words are not the command's: the name of the command gives the switch its meaning, and the user passes no
  arguments at all.
- `reboot` runs two modules in a row, `demo-switch` with a cycle of the power line and `demo-console`, and
  configures each use with options of its own: `cycle` sets the length of the power cycle, `expect` ends the console
  once the board printed its welcome line. The command `console` uses the same module without the option, so its
  console runs until the user ends it.
- `update` declares the argument `image` and hands it to the modules that need it, `${image}` in their arguments;
  `wrap-print` tells the user what the command does.
- `flash` passes the user's arguments through to `demo-flash`, so the module's own syntax is the command's.

The commands of the device `agent` are the plain case, one module each. They show the three ways a command hands
arguments to a module: `echo`, `upload` and `download` pass the user's arguments through, `hello` declares the
argument `who` and places it in a longer one, `Hello, ${who}!`, and `fail` and `crash` fix the arguments in the
configuration.

## What the modules show

Together the modules use every feature of [module.Session](../module.go), and the integration tests of DUT Control run
against them:

| Session feature                                                     | Module                        |
|---------------------------------------------------------------------|-------------------------------|
| Print, Printf, Println                                              | Print, Switch, Flash, Upload  |
| Line console, lines assembled from what the module reads            | Prompt                        |
| Raw console                                                         | Raw, Console                  |
| Print while a console is open                                       | Prompt, Raw                   |
| The user's input ends, the output goes on                           | Raw, Console                  |
| The session is torn down, a write fails with io.ErrClosedPipe       | Raw, Console                  |
| Stdin.Close ends a read on another goroutine                        | Console                       |
| RequestFile, a file the user cannot provide                         | Upload, Flash                 |
| SendFile                                                            | Download, Flash               |
| An error returned by Run                                            | Fault, Switch, Flash          |
| A panic in Run, recovered by the agent                              | Fault                         |
| The context ends when the command is cancelled                      | Switch                        |
| State that modules share beyond their own fields, guarded by a lock | the board, see `board.go`     |

# Print

Prints its arguments, separated by spaces, as one line. It shows what a command hands to a module, and lets a step of
a command say what the command does.

```
ARGUMENTS:
	[text...]
```

## Configuration Options

_none_

# Prompt

Asks for your name in a line console and greets you. Your terminal edits the line, and Enter sends it; Ctrl-D at the
start of the line ends the input unanswered, and Ctrl-C ends the command. Piped input works too:
`printf 'Ada' | dutctl agent prompt`.

```
ARGUMENTS:
	none
```

## Configuration Options

_none_

# Raw

Shows the bytes your keys send in a raw console, as one line of hex bytes per chunk the module receives. Every key
arrives at once, as it is: Enter as `0d`, Ctrl-C as `03`, an arrow key as an escape sequence. A lone Ctrl-D ends the
console.

```
ARGUMENTS:
	none
```

## Configuration Options

_none_

# Upload

Receives a file from your machine and tells its size and SHA-256.

```
ARGUMENTS:
	<file>
```

## Configuration Options

_none_

# Download

Sends a short text file to your machine, saved under the name you pass.

```
ARGUMENTS:
	<file>
```

## Configuration Options

_none_

# Fault

Fails on purpose, as a module with a bug or a broken device does, to show what dutctl reports and that the agent goes
on serving.

```
ARGUMENTS:
	error|panic [message...]

  error  returns an error, as a module does when its device fails
  panic  panics, as a module with a bug might; the agent recovers it
```

## Configuration Options

_none_

# Switch

Switches a line of the board, the one the option `line` wires it to, or shows it. It knows the line, not what the
line is for: switched on, the power line boots the board, which its console shows, and the write-protect line keeps
the flash from being written.

```
ARGUMENTS:
	[on|off|toggle|cycle|status]

  on      switches the line on
  off     switches the line off
  toggle  switches the line to its other state
  cycle   switches the line off and, after the configured time, on again
  status  shows whether the line is on; the default

Ctrl-C during a cycle aborts it and leaves the line off.
```

## Configuration Options

| Option | Value                  | Description                                             |
|--------|------------------------|---------------------------------------------------------|
| line   | power or write-protect | The line of the board the switch is wired to; required  |
| cycle  | Go duration            | How long a cycle keeps the line off; 1s if unset        |

# Console

Bridges your terminal to the board's serial console. The board does the echo and the line editing, so it is a raw
console: every key reaches the board at once, Ctrl-C included. While the board is on, its shell knows `help`,
`version`, `echo <text>` and `exit`. `exit` ends the session, and so do Ctrl-A x and the end of piped input: with the
board on, `printf 'version\r' | dutctl demo-board console` prints the firmware's version. A board that is off stays
silent. What the board printed while no console was attached, the boot messages for instance, is shown first. Each
piece of the board's output reaches one console only, so with two consoles open, each shows a part of it.

The markers `--- Connected ...` and `--- Connection closed ---` go to standard error, so that standard output holds
what the board sent and nothing else.

```
ARGUMENTS:
	none
```

## Configuration Options

| Option | Value | Description                                                                                       |
|--------|-------|---------------------------------------------------------------------------------------------------|
| expect | text  | Ends the console once the board printed this text; unset, the console runs until the user ends it |

# Flash

Writes a firmware image from your machine to the board's flash, or reads the flash into a file on your machine. The
board must be off and its flash not write-protected for a write, and the image must fit the flash of 1 MiB. The board
boots what is in its flash; its console shows the image's name and the start of its SHA-256.

```
ARGUMENTS:
	write <image> | read <file>
```

## Configuration Options

_none_
