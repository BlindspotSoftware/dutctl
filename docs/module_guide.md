# Module Plug-In System

In DUT Control, modules represent the implementation of actions to be performed on a device. One or more Modules make
up a command that can be issued to a device. The implementation of a Module determines its capabilities and also exposes
information on how to use and configure it.

The DUT Control project is designed for easy integration of new modules via a plug-in system.

> [!NOTE]
> The modules generally available at a running dutagent instance are set at the compile time of dutagent.
> Which Modules are used with certain devices is controlled via the dutagent configuration when starting dutagent.

## The Module interface

Modules must implement the following interface:

```go
type Module interface {
  Help() string
  Init(ctx context.Context) error
  Deinit(ctx context.Context) error
  Run(ctx context.Context, s Session, args ...string) error
}
```

See [`pkg/module/module.go`](../pkg/module/module.go) for further information on the set of functions.
`Run` must return promptly once its context is cancelled (the command was aborted or the client disconnected):
until it returns, the device stays busy, even for the user who aborted the command, and a forced unlock does not free it.
With the _Session_ provided to the module, it is able to interact with the client during execution (status messages,
request input, file transfer, etc.).

## Console

A console lets a module interact with the user through the client's standard streams. A module opens one with
`OpenConsole` on its _Session_ and receives a `Console` value with the three streams:

```go
func (m *MyModule) Run(ctx context.Context, s module.Session, args ...string) error {
  con := s.OpenConsole(module.ConsoleOptions{Mode: module.ConsoleRaw})

  // con.Stdin  io.ReadCloser  the user's input
  // con.Stdout io.Writer      output shown on the user's standard output
  // con.Stderr io.Writer      output shown on the user's standard error
  ...
}
```

The mode tells the client how the module uses its console, so the client can configure the user's terminal. Only the
module can know, so choose deliberately:

- `module.ConsoleLine` (the zero value of `ConsoleOptions`) is a console of text lines. The user's terminal does the
  echo and the line editing, Enter sends the line, and Ctrl-C still interrupts the client. `Stdin` delivers the bytes
  as the client read them, in chunks that may hold several lines or end in the middle of one, so assemble lines with a
  `bufio.Reader`; a line the user entered on a terminal ends in `'\n'`, a pipe's last one may not. That is one round
  trip per line, and it works with pipes and in CI just as well as on a terminal. Pick it for prompts,
  question-and-answer interaction and input piped to a process.
- `module.ConsoleRaw` is a console of bytes for bridging a terminal line, such as a serial port or a pty. Every byte
  reaches `Stdin` as typed and at once, with no local echo: Enter arrives as CR, Ctrl-C as the byte `0x03`, Escape as
  `0x1b`. Bytes written to `Stdout` reach the user's terminal unchanged, so the far end is in charge of echo and line
  discipline.

The contract of a console, in short (see the doc comments in [`pkg/module/module.go`](../pkg/module/module.go) for
the full version):

- `Stdin` returns `io.EOF` when the user's input ended, which is the normal end of the input, not a failure: a module
  that reads its console to the end returns `nil`.
- `Stdin.Close` ends the input from any goroutine, also while a `Read` blocks, and leaves the output open.
- `Stdin` has one reader at a time, which may run on a goroutine other than `Run`'s. `Stdout` and `Stderr` are safe
  for concurrent use.
- A write fails with `io.ErrClosedPipe` once the console ended or the session was torn down. That is a normal end as
  well, not a module failure.
- The console closes when `Run` returns. Stop every goroutine that uses it before returning.
- A module has one console at a time: a second `OpenConsole` closes the first, whose `Stdin` then reports `io.EOF`
  and whose writers fail.
- `Print`, `Printf` and `Println` may be used while a console is open; the client shows both in the order sent.
- A module that opened a console must keep reading `Stdin` or close it: while a console is open, input waits for the
  module to read it, and a file transfer waits behind it.
- A raw console is served cooked when the client's standard input is not a terminal: the input arrives as it is read,
  in chunks, with no raw mode anywhere. Users who pipe input into a raw console have to send CR themselves if the far
  end needs it.

The [project's dummy modules](../pkg/module/dummy) contain the smallest example in both modes, the `dummy-console`
module with a raw, a hex and a line demo. The serial module's `-i` mode is the real bridge of a terminal line to a
raw console.

## Registration

New modules go under `pkg/modules'. 

To register a module for use in _dutagent_, modules must call `module.Register()` and provide its name and a
constructor. By convention, this is done in the module's `init()` function. E.g.:

```go
func init() {
  module.Register(module.Info{
    ID:  "reset",
    New: func() module.Module { return new(power.Reset) },
  })
}
```

`ID` is the module's unique identifier. This string is used in the [_dutagent_ configuration](./dutagent-config.md) to refer to this
module implementation.

`New` is a function to instantiate an instance of the module. Usually it can be as simple as shown above.
Note that initial setup code can be placed in the `Init()` function of the module interface, which supports error
checking and should be preferred over the constructor for most setup code.

With this in place, the _dutagent_ can use modules by using anonymous imports, e.g.:

```go
_ "github.com/BlindspotSoftware/dutctl/pkg/module/dummy"
```

## Configuration
A module can be dynamically configured when starting a _dutagent_ using the `with` map in the
[_dutagent_ configuration](./dutagent-config.md#module). A module must be of type `struct` and have the configuration as
fields. The parser will set the struct fields to match the map keys.

For example, a module like the one below, registered with `ID` = `"my-module"`.

```go
type MyModule struct {
  Foo int    
}
```

It can be configured with:

```yaml
---
version: 0
devices:
  some-device:
    desc: Example device
    cmds:
      some-cmd:
        desc: My cool module
        uses:
          - module: my-module
            with:
              foo: 42
```

> [!IMPORTANT]  
> It is imperative that the module's documentation and Help() function provide a good explanation of its configuration.
> The `with` map in the configuration file is generic (string → any type), so it is important that the user knows what
> values are expected.

The [project's dummy modules](../pkg/module/dummy/dummy_status.go) show all the details of a complete implementation.
