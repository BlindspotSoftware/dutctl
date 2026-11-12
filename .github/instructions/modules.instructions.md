---
applyTo: "pkg/module/**"
---

# Review rules for modules

A module provides one function for dutagent. It implements `module.Module` from
`pkg/module/module.go`, whose doc comments are the module contract, and reaches the user only
through the `module.Session` passed to `Run`. The demo modules in `pkg/module/demo` show the
expected form. One module instance serves every run of its command, and runs of other instances,
also of the same type, overlap with it.

## Help, options and Init

- [MOD-1] `Help` describes the module's arguments and this instance's configuration: the
  configured values and the defaults in effect, not one generic text for every configuration. For
  the command's passthrough module, `Help` is the command's help.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/pkg/module/module.go
- [MOD-2] Each `with` option is an exported struct field, matched by its lower-cased name, so it
  needs no YAML tag. Derived and internal state goes into unexported fields.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/dutagent-config.md#module
- [MOD-3] `Init` checks the options and applies the defaults of unset ones. A missing or invalid
  option fails `Init` with an error, which stops the agent's start, also with `-dry-run`. A field
  may instead be marked required with a `validate` struct tag, which the agent checks when it
  loads the configuration.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0021-contain-module-faults-to-their-own-command.md#decision-outcome
- [MOD-4] `Init` never contacts the device or its BMC: no dial, no open serial port, no BMC or
  HTTP session. The module connects in `Run` or on first use. Checking what the module needs on
  the agent, such as a tool on PATH, a key file or hardware attached to the agent, belongs in
  `Init`, so that `dutagent -dry-run` reveals what is missing. Setting up hardware on the agent's
  own host is fine too, as the GPIO modules drive their pins to an initial level.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0021-contain-module-faults-to-their-own-command.md#decision-outcome
- [MOD-5] `Deinit` is safe to call when `Init` never ran or failed partway, and releases what the
  module still holds: what `Init` set up and state kept across runs on purpose, such as a
  connection opened on first use. It never runs beside `Run`. Nothing may rely on it running:
  after a killed agent, the next `Init` finds the hardware as it was left.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/dutagent-shutdown.md#when-deinit-runs

## Run

- [MOD-6] `Init`, `Run` and `Deinit` return promptly once their context is done (GEN-13); where a
  blocking call takes no context, close its resource from a watcher. A wait on the console's
  streams needs no context: a cancelled run tears the session down, which ends `Stdin` with
  `io.EOF` and fails writes. Until `Run` returns, its device stays busy, and a forced unlock does
  not free it. Do not promise the agent's current time limits for `Init` and `Deinit` as numbers.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0027-cancel-work-through-contexts-without-a-deadline-on-run.md#decision-outcome
- [MOD-7] External tools start through `internal/procexec`, not `exec.Command` or
  `exec.CommandContext`, which leave the tool's children running and `Run` waiting.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0027-cancel-work-through-contexts-without-a-deadline-on-run.md#decision-outcome
- [MOD-8] Per-run state lives in `Run`'s locals: parsed arguments, the operation, temporary paths,
  connections. Struct fields hold the configuration, what `Init` derives from it, and state kept
  across runs on purpose. Guard with a lock whatever modules share beyond their own fields, such as
  a package variable or a library's global state (the GPIO register mapping, the demo board).
  Hardware that two commands share, such as a serial port, a GPIO pin, a PDU outlet or a BMC, is
  the admin's responsibility; do not ask a module to lock it.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0036-run-a-users-different-commands-side-by-side.md#decision-outcome
- [MOD-9] A run's scratch files go into a directory made with `os.MkdirTemp("", "<module>-")` in
  `Run`, held in a local and removed in a `defer`, so it also goes after an error, a cancellation
  or a panic. Never a fixed path such as `./image`, and never create `$TMPDIR` itself: it must
  exist. Close every file `Run` opens; a failed `Close` of a file it wrote is a failed write.
  Destinations named by the user or the configuration are not scratch files.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0037-keep-the-files-of-a-run-in-a-directory-of-its-own.md#decision-outcome
- [MOD-10] `Run` reports every failure, missing or unknown arguments included, by returning an
  error. Flag a module that prints an error and returns nil, or prints the error it returns.
  Return errors rather than panic: the agent recovers a panic in `Run` itself, but not in a
  goroutine that `Run` starts, which ends the agent with every run on it.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0004-compile-modules-into-the-agent-behind-a-session.md#decision-outcome
- [MOD-11] `Run` never expects `help` as its first argument, and does not assume it is the
  passthrough module: the command configuration may fix or template its arguments.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0016-route-arguments-through-passthrough-or-templates.md#decision-outcome
- [MOD-12] A module that takes paths from the command line decides which it accepts and checks
  them itself: the client reads and writes whatever path the agent names. The file module, for
  example, accepts a destination from the command line only as a relative path without a leading
  `..`. A client path that the user typed and the module passes unchanged to `RequestFile` or
  `SendFile`, as flash and the demo modules do, is the user's own choice.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0014-let-the-agent-access-client-files-the-user-did-not-name.md#decision-outcome

## Session and console

- [MOD-13] `OpenConsole`, `Print`, `Printf`, `Println`, `RequestFile` and `SendFile` are called
  from `Run`'s goroutine only. Other goroutines may only read the console's `Stdin` (one reader at
  a time), close it, and write its `Stdout` and `Stderr`; stop every goroutine that uses the
  console before `Run` returns.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/pkg/module/module.go
- [MOD-14] A console opens in the mode the module needs: `ConsoleLine`, the zero value, for
  prompts and piped input; `ConsoleRaw` for bridging a terminal line, such as a serial port. The
  module depends neither on raw mode nor on how input is chunked. While its console is open, it
  keeps reading `Stdin` or closes it. `io.EOF` on `Stdin` and `io.ErrClosedPipe` on a write are the
  console's normal end, so `Run` returns nil unless something else failed.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/module_guide.md#console

## Logging, scope and documentation

- [MOD-15] Modules log through `log.FromContext(ctx)` for the operator who wired the device: the
  external interactions with their effective parameters (tool and command line, host and port,
  device and settings) and the defaults applied for missing configuration. No lifecycle markers
  such as "Run called" or "init done", and nothing the user already sees through the Session.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/CONTRIBUTING.md#agent-and-server-logging
- [MOD-16] A module provides one function, such as switching a line, writing a flash or running
  a tool, and stays reusable through its options, such as the line, port or tool; the command
  configuration gives it its meaning. Scheduling, queues and image stores do not belong in a
  module (GEN-5).
  → https://github.com/BlindspotSoftware/dutctl/blob/main/pkg/module/demo/README.md#modules-and-commands
- [MOD-17] A new module registers from its package's `init` with `module.Register`, keeps `New`
  free of side effects, and gets a blank import in `cmds/dutagent/modules.go`. When a module's
  arguments or options change, its README, its `Help` and its example configuration change in
  the same pull request.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/module_guide.md#registration
- [MOD-18] New module behavior and module bug fixes come with unit tests in the same change; a
  test that needs a `Session` uses `internal/test/mock.Session`. A change to a demo module keeps
  the feature table in `pkg/module/demo/README.md` and the tests in `test/integration` in step.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/pkg/module/demo/README.md#what-the-modules-show

## Do not flag

- A `yaml` tag that equals the field's lower-cased name: redundant, but harmless.
- An empty `Init` or `Deinit` with a doc comment that says there is nothing to set up.
- A `select` on console streams without `ctx.Done()` (MOD-6), as in `pkg/module/demo/console.go`.
