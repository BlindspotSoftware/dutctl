# Add a module to dutagent

In DUT Control, modules represent the implementation of actions to be performed on a device. One or more modules make
up a command that can be issued to a device. This guide shows how to write a module, register it with dutagent, make
it configurable and let it talk to the user. The doc comments of the [module API](../pkg/module/module.go) state what
each part of it must and must not do.

> [!NOTE]
> The modules generally available at a running dutagent instance are set at the compile time of dutagent.
> Which Modules are used with certain devices is controlled via the dutagent configuration when starting dutagent.

## Write the module

A module is a Go type that implements `module.Module`: `Help`, `Init`, `Deinit` and `Run`. With the _Session_ that
`Run` gets, the module interacts with the client during execution: status messages, a console, file transfers. The
[example of the module API](../pkg/module/example_test.go) is a complete module with one option:

<!-- embed: pkg/module/example_test.go -->

## Registration

New modules go under `pkg/module/<name>`.

To register a module for use in _dutagent_, modules must call `module.Register()` and provide its name and a
constructor. By convention, this is done in the module's `init()` function, as in the example above.

`ID` is the module's unique identifier. This string is used in the [_dutagent_ configuration](./dutagent-config.md) to
refer to this module implementation.

`New` is a function to instantiate an instance of the module. Usually it can be as simple as shown above. Note that
initial setup code can be placed in the `Init()` function of the module interface, which supports error checking and
should be preferred over the constructor for most setup code.

With this in place, add the package to the anonymous imports of dutagent in
[cmds/dutagent/modules.go](../cmds/dutagent/modules.go):

```go
_ "github.com/BlindspotSoftware/dutctl/pkg/module/<name>"
```

## Configuration

A module can be dynamically configured when starting a _dutagent_ using the `with` map in the
[_dutagent_ configuration](./dutagent-config.md#module). A module must be of type `struct` and have the configuration as
exported fields. The parser will set the struct fields to match the map keys, in lower case.

The example module above, registered with `ID` = `"greet"`, can be configured with:

```yaml
---
version: 0
devices:
  some-device:
    desc: Example device
    cmds:
      greet:
        desc: Greet people
        uses:
          - module: greet
            passthrough: true
            with:
              greeting: Hi
```

> [!IMPORTANT]
> It is imperative that the module's documentation and Help() function provide a good explanation of its configuration.
> The `with` map in the configuration file is generic (string → any type), so it is important that the user knows what
> values are expected. Describe the module in a README of the structure that
> [CONTRIBUTING.md](../CONTRIBUTING.md#documentation-style-guide) gives.

## Console

A console lets a module interact with the user through the client's standard streams. A module opens one with
`OpenConsole` on its _Session_ and states its mode, so that the client can set up the user's terminal:

- `module.ConsoleLine`, the zero value, for prompts, question-and-answer interaction and input piped to a process. The
  user's terminal edits the line, and the module assembles lines from what it reads.
- `module.ConsoleRaw` for bridging a terminal line, such as a serial port or a pty: every byte goes through as typed.

The [demo module](../pkg/module/demo/README.md) `wrap-prompt` asks for a line in a line console:

<!-- embed: pkg/module/demo/wrap_prompt.go#Run -->

`wrap-raw` is the smallest raw console, `demo-console` a bridge to a simulated serial console, and the serial module's
`-i` mode the real bridge of a terminal line. What a module must do with an open console, such as reading its input to
the end or closing it, is part of the doc comments of `OpenConsole` and `ConsoleMode` in the
[module API](../pkg/module/module.go).

## Transfer files

`RequestFile` gets a file from the user's machine, `SendFile` sends one to it. The demo modules `wrap-upload` and
`wrap-download` show each on its own, and `demo-flash` both in a module for a device; the
[demo modules' README](../pkg/module/demo/README.md#what-the-modules-show) lists which module shows which feature of
the session.
