# Control a simulated board

In this tutorial we run a dutagent on our own machine with a simulated board, and control it with dutctl: we list
devices and commands, switch the board on, watch it boot on its serial console, write new firmware to its flash, and
reserve it. No hardware is needed. It takes about 15 minutes.

You need a Linux machine with `git` and [Go](https://go.dev/dl/) 1.26.7 or later, and three terminals.

## Build dutctl and dutagent

In the first terminal, get the source and build both programs:

```sh
git clone https://github.com/BlindspotSoftware/dutctl.git
cd dutctl
go build -o bin/ ./cmds/dutctl ./cmds/dutagent
export PATH="$PWD/bin:$PATH"
```

## Start an agent

Start dutagent with the configuration of the [demo modules](../pkg/module/demo/README.md):

```sh
dutagent -c pkg/module/demo/demo-example-cfg.yml
```

The agent logs that it is listening, and keeps running:

```text
INFO  [agent] loading configuration path=pkg/module/demo/demo-example-cfg.yml
INFO  [agent] module initialization complete
INFO  [agent] rpc service listening addr=127.0.0.1:2024
```

Leave it running. For the rest of the tutorial, use the second terminal; run the same `cd` and `export` there first.

## Find a device and its commands

Ask the agent which devices it serves:

```console
$ dutctl list
- agent
- demo-board
```

Each device has commands. Ask for the commands of `agent`:

```console
$ dutctl agent
- crash
- download
- echo
- fail
- hello
- keys
- prompt
- upload
```

Run two of them:

```console
$ dutctl agent echo hello world
hello world
$ dutctl agent hello Ada
Hello, Ada!
```

Notice that `hello` takes a name. `help` after a command shows what it takes:

```console
$ dutctl agent hello help
Command with 1 module(s): wrap-print

Arguments:
  who: The name to greet
```

## Watch the board boot

The device `demo-board` is a simulated board with a power line, a flash and a serial console. Open its console:

```console
$ dutctl demo-board console
--- Connected to the demo board, which is off: power it on to watch it boot ---
```

The console stays silent, because the board is off. Leave it open, and switch the power on in the third terminal:

```console
$ dutctl demo-board power on
The power line is now on.
```

The board boots in the second terminal:

```text
Demo board boot ROM 1.0
Booting factory.bin, 24 bytes, sha256 ad98528868d6
Welcome to the demo board. Type help for its commands.
demo#
```

Type `help` and Enter there, and the board's shell answers. Every key you type goes to the board, Ctrl-C included.

## Write new firmware

Back in the third terminal, write a file to the board's flash as its new firmware. Any file will do; we use the README:

```console
$ dutctl demo-board update README.md
Updating the board with README.md.
The power line is now off.
↑ sent "README.md" (9.2 KiB)
Wrote README.md, 9381 bytes, sha256 1f5b316edb3f to the flash.
The power line is now on.
```

The console in the second terminal shows the board lose power and boot the new firmware:

```text
[the board lost power]

Demo board boot ROM 1.0
Booting README.md, 9381 bytes, sha256 1f5b316edb3f
```

Now protect the flash, and try the update again:

```console
$ dutctl demo-board protect
The write-protect line is now on.
$ dutctl demo-board update README.md
Updating the board with README.md.
The power line is now off.
✗ module failed: the flash is write-protected: switch its write-protect line off before writing it
```

The command stopped at the flash step and left the board off. Lift the protection again:

```console
$ dutctl demo-board unprotect
The write-protect line is now off.
```

## Reserve the board

Other people may use the same agent. Reserve the board for yourself:

```console
$ dutctl demo-board lock
✓ Device "demo-board" locked by "ada@lab" for 30m
$ dutctl list
- agent
- demo-board [locked by "ada@lab" for 30m]
```

Your name is your user and host name, here `ada@lab`. Release the board:

```console
$ dutctl demo-board unlock
✓ Device "demo-board" unlocked
```

## Finish

End the console in the second terminal with Ctrl-A x, and stop the agent in the first one with Ctrl-C.

We have run an agent, found its devices and commands, watched a board boot on its console while we switched its power
from another terminal, written firmware to its flash, and reserved the board. A real agent works the same way, with
modules that drive real hardware in place of the simulation.

Next:

- Run `dutctl` without arguments on a terminal: it opens the [TUI](dutctl-tui.md), which walks from hosts to devices
  to commands.
- [Run your commands side by side](run-commands-side-by-side.md), as we did with the console and the power.
- Learn [how dutctl is built](architecture.md): the client, the agent and the modules behind a command.
