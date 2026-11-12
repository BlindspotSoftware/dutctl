# Decision records and their constraints

One entry per ADR in `docs/decisions/`: what it governs, and the constraint it puts on code.
Each entry summarizes the ADR's Decision outcome, with its later changes; the ADR is the source.
Links point to the ADR's Decision outcome.

## Architecture and protocol

- **ADR-0001 Split DUT control into an agent and a thin client**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0001-split-dut-control-into-an-agent-and-a-thin-client.md#decision-outcome
  Governs `cmds/dutctl`, `cmds/dutagent`. Only dutagent knows the devices, their commands and the
  modules behind them; dutctl asks the agent and keeps no state of its own. Every exchange between
  dutctl and an agent starts with a dutctl request; the agent's registration with the experimental
  dutserver is the only call an agent starts.
- **ADR-0002 Speak gRPC over cleartext HTTP/2 with connect-go**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0002-speak-grpc-over-cleartext-http-2-with-connect-go.md#decision-outcome
  Governs `protobuf/`, `internal/rpc`, `go.mod`. connect-go, not grpc-go, with the gRPC protocol
  over h2c on plain `net/http` servers. After a `.proto` change, run buf lint and buf generate and
  commit the generated code under `protobuf/gen`.
- **ADR-0003 Run a command over one bidirectional stream**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0003-run-a-command-over-one-bidirectional-stream.md#decision-outcome
  Governs `protobuf/dutctl/v1`, `cmds/dutagent/run.go`, `cmds/dutctl/rpc.go`,
  `internal/dutagent/session`. Discovery RPCs are unary; a command runs over one bidirectional
  Run stream whose first request carries the command. The module on the agent starts every file
  transfer, one at a time; a file moves whole in one message and is held in memory on both sides.
  The agent fails a run on a file it did not request. The stream ends when the agent's handler
  returns; there is no end-of-run message.
- **ADR-0004 Compile modules into the agent behind a Session**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0004-compile-modules-into-the-agent-behind-a-session.md#decision-outcome
  Governs `pkg/module`, `cmds/dutagent/modules.go`. A module is a Go package under
  `pkg/module/<name>` that registers itself from `init` with `module.Register` and is linked in by
  a blank import in `cmds/dutagent/modules.go`. It reaches the user only through the
  `module.Session`, never the stream, and reports a failure by returning an error from `Run`
  without also printing it.
- **ADR-0007 Route module input and output through a session**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0007-route-module-input-and-output-through-a-session.md#decision-outcome
  Governs `internal/dutagent/session`, `internal/chanio`, `pkg/module`. Per run, a broker builds
  the Session from unbuffered channels, one per kind of message (only a single file's channel holds
  one message), with one worker per direction, and reaches the stream only through its `Stream`
  interface. Modules never see the stream, protobuf messages or connect. The Print family and
  `OpenConsole` return no error: a failed delivery fails the run instead. `RequestFile` and
  `SendFile` return an error, and after a teardown console input ends and writes fail with
  `io.ErrClosedPipe`.
- **ADR-0024 Keep dutctl a hardware-control layer**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0024-keep-dutctl-a-hardware-control-layer.md#decision-outcome
  Governs everything. An agent offers named commands per device built from modules, the calls to
  discover them, and one lock per device. No test framework, job scheduler or queue, idle power
  policy or firmware image management; a tool on top gets information through the RPC API
  instead.
- **ADR-0034 Version the relay protocol as alpha**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0034-version-the-relay-protocol-as-alpha.md#decision-outcome
  Governs `protobuf/dutctl/relay`, `cmds/exp/dutserver`. The relay service and its messages live
  in package `dutctl.relay.v1alpha1`, outside the compatibility promise; `dutctl.v1` keeps only
  `DeviceService` and its messages. Once the relay is ready for the promise, its protocol moves to
  `dutctl.relay.v1`.

## Configuration and command line

- **ADR-0005 Configure devices and commands in one YAML file**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0005-configure-devices-and-commands-in-one-yaml-file.md#decision-outcome
  Governs `pkg/dut`, `pkg/module`, `docs/dutagent-config.md`. dutagent reads its whole setup from
  one YAML file at start, decodes a module's options into the exported fields of its struct by
  their lower-cased names, so modules need no YAML tags or parsing code, and validates them. A
  file that does not decode or validate stops the agent at start.
- **ADR-0009 Initialize every module once at agent start**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0009-initialize-every-module-once-at-agent-start.md#decision-outcome
  Governs `cmds/dutagent/lifecycle.go`, `pkg/module`. dutagent calls `Init` on every module once
  at start, also after one failed, reports all failures together, and any failure aborts the
  start. `Deinit` runs at most once: when the agent stops and no command runs any more, or when the
  start fails after `Init`; never after a command, and not at all when the agent is killed or exits
  at once in stage 3. A module instance serves every run of its command, so state kept in its
  struct during `Run` carries over.
- **ADR-0010 Use a positional CLI grammar with reserved keywords**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0010-use-a-positional-cli-grammar-with-reserved-keywords.md#decision-outcome
  Governs `cmds/dutctl`, `internal/keyword`, `pkg/dut`. Flags come before the positional words,
  and the words after the command go to it unchanged. A keyword is reserved only in its ambiguous
  position: `list` and `version` as a device; `lock`, `unlock` and `help` as a command; `help`
  alone after a command; `force` only after `unlock`. All reserved words live in
  `internal/keyword`, and a new one in the device or command position is breaking.
- **ADR-0015 Name the configuration keys after GitHub Actions**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0015-name-the-configuration-keys-after-github-actions.md#decision-outcome
  Governs `pkg/dut`, `docs/dutagent-config.md`. A command lists its modules under `uses`, a module
  entry takes its options under `with`; `args`, `desc`, `cmds` and `module` keep their names, and
  the old keys get no aliases. The Go fields keep their names: `Command.Modules` and
  `ModuleConfig.Options` map to `uses` and `with` through YAML tags.
- **ADR-0016 Route arguments through passthrough or templates**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0016-route-arguments-through-passthrough-or-templates.md#decision-outcome
  Governs `pkg/dut`, `docs/command-arg-templating.md`. A command takes no arguments (it refuses
  runtime arguments before any module runs), passes them all to at most one `passthrough: true`
  module without fixed `args` (whose help is the command's help), or maps exactly as many runtime
  arguments as it declares `args`, by position, into `${name}` references. Not both; an undeclared
  reference fails at load. No template engine.
- **ADR-0023 Reject RPCs from another major version**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0023-reject-rpcs-from-another-major-version.md#decision-outcome
  Governs `internal/compat`, `internal/rpc/version.go`, `pkg/headers`, `cmds/exp/dutserver`. Both
  ends send their version in the `Dutctl-Version` header. The agent rejects another major version
  with FailedPrecondition before any handler runs, and the dutserver relay refuses such a peer
  itself. The client never fails over the version: it warns once about a different minor version, a
  different pre-release tag, or a missing or unparsable version, and is silent on a patch
  difference. `internal/compat` holds the thresholds but no message wording.
- **ADR-0028 Tier the public interfaces for v1.0.0**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0028-tier-the-public-interfaces-for-v1-0-0.md#decision-outcome
  Governs `docs/dutagent-config.md`, `pkg/dut`, `protobuf/dutctl/v1`. Primary: the configuration
  grammar and the RPC protocol `dutctl.v1`, joined by the module API (ADR-0033); they become
  stable with v1.0.0, and a change to them needs an ADR. Secondary, best effort: the command-line
  interfaces.
- **ADR-0033 Add the module API to the primary public interfaces**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0033-add-the-module-api-to-the-primary-public-interfaces.md#decision-outcome
  Governs `pkg/module/*.go`, `docs/module_guide.md`. Every exported name in `pkg/module` and the
  behavior its doc comments state are primary interface. A new method on `Module` or `Session` is
  breaking. Time limits are not promised as numbers: modules may rely on `Init`'s and `Deinit`'s
  contexts carrying a deadline, and on `Init`'s context also ending when the agent is asked to stop
  during startup, but not on how long the agent waits.

## Modules

- **ADR-0014 Let the agent access client files the user did not name**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0014-let-the-agent-access-client-files-the-user-did-not-name.md#decision-outcome
  Governs `pkg/module`, `cmds/dutctl/rpc.go`. The client reads and writes whatever path the agent
  names, without checking it against the command's arguments; it creates new files with mode 0600
  and overwrites existing ones. A module that takes paths from the command line decides which it
  accepts and checks them itself; the core does not. The file module, for example, takes
  command-line destinations only as relative paths without a leading `..`, and sources as given.
- **ADR-0021 Contain module faults to their own command**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0021-contain-module-faults-to-their-own-command.md#decision-outcome
  Governs `pkg/module`, `cmds/dutagent`. `Init` validates the configuration and what the module
  needs on the agent, such as a tool on the `PATH` or a key file, and never reaches out to the DUT
  or its BMC: no dial, no serial port, no BMC session; the module connects per run or on first use.
  Setting up hardware on the agent's own host is fine: the GPIO modules drive their pins to an idle
  or initial level in `Init`. The agent recovers panics in `Init`, `Run` and `Deinit`; modules
  still return errors rather than panic.
- **ADR-0027 Cancel work through contexts, without a deadline on Run**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0027-cancel-work-through-contexts-without-a-deadline-on-run.md#decision-outcome
  Governs `pkg/module`, `internal/procexec`, `internal/rpc`, `cmds/dutctl`, `cmds/dutagent`.
  Every layer passes its context on, and every blocking call takes it or selects on it. Modules
  return promptly when their context is done and start external tools through
  `internal/procexec`. Each unary RPC of dutctl gets a 30-second deadline, and the agent's
  registration with a dutserver one of 10 seconds; Run gets none. The HTTP transport bounds only
  dialing and idle pooled connections, and the agent's server only the reading of request headers;
  no other client or server timeout may bound Run.
- **ADR-0035 Frame consoles in line or raw mode**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0035-frame-consoles-in-line-or-raw-mode.md#decision-outcome
  Governs `protobuf/dutctl/v1`, `internal/dutagent/session`, `cmds/dutctl`, `pkg/module`. A
  module opens a console in LINE (the zero value) or RAW mode. The agent frames it with
  `ConsoleOpen`, which carries the mode and an id unique within the run, and ends it with
  `ConsoleClose`; `ConsoleOutput` carries the module's stdout and stderr, and the client marks
  `ConsoleInput` and `ConsoleControl` (eof) with the console's id. The client forwards input only
  while a console is open, the agent drops input not marked for the open console, and no hop
  alters the bytes. A module relies neither on raw mode nor on how input is chunked, and keeps
  reading its input or closes it.
- **ADR-0037 Keep the files of a run in a directory of its own**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0037-keep-the-files-of-a-run-in-a-directory-of-its-own.md#decision-outcome
  Governs `pkg/module`. A run's scratch files go into a directory made with
  `os.MkdirTemp("", "<module>-")` in `Run`, held in a local, and removed when `Run` returns, also
  after a failure, a cancellation or a panic. Every opened file is closed, and a failed close of a
  written file is a failed write. Modules never create the temporary directory itself. The rule
  covers scratch files only: a destination that the user or the configuration names, as with the
  `file` module, and state kept across runs on purpose are not scratch files.

## Runs, locks and stopping

- **ADR-0017 Lock devices in agent memory**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0017-lock-devices-in-agent-memory.md#decision-outcome
  Governs `internal/dutagent/locker`, `cmds/dutagent/rpc.go`. Each agent keeps its devices' locks
  in memory only. A running command's hold has no expiry and ends with the command; no heartbeats,
  no maximum lock duration. A refused run or reservation gets FailedPrecondition naming the
  holder. Locks coordinate cooperating users and are no security boundary.
- **ADR-0018 Identify callers by the HTTP From header**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0018-identify-callers-by-the-http-from-header.md#decision-outcome
  Governs `cmds/dutctl`, `internal/rpc/identity.go`, `internal/auth`, `pkg/headers`. The client
  sends the caller's self-asserted name in the `From` header (`headers.User`); the agent takes it
  unauthenticated. A caller without it gets `unknown-` plus a random suffix new on every request.
  Request messages carry no caller identity.
- **ADR-0025 Map errors to connect codes only in RPC handlers**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0025-map-errors-to-connect-codes-only-in-rpc-handlers.md#decision-outcome
  Governs all Go code. Packages return their own sentinels and types, wrapped with context, and
  document which callers may match; third-party error types are not part of that contract. Only
  the handlers of dutagent and dutserver, with the mapping helpers in `cmds/dutagent/errmap.go`,
  and the version interceptor assign connect codes, after the ADR's table (CORE-12 in
  `.github/instructions/core.instructions.md` has it); each dutagent handler that can fail lists
  its codes in an `Errors:` paragraph. An error is logged once, where it becomes terminal; a lower
  layer may log one detail line in its own scope, marked as deliberate by a comment.
- **ADR-0026 Defer authentication to mTLS**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0026-defer-authentication-to-mtls.md#decision-outcome
  Governs `cmds/dutagent`, `internal/rpc/identity.go`, `internal/auth`. The interceptor
  `rpc.NewIdentifier` resolves the caller once per request into an `auth.Identity` on the context;
  handlers read it with `auth.FromContext` and never look at headers. Restricting `unlock force`
  and keeping locks across restarts wait for authenticated callers.
- **ADR-0029 Run the Run RPC as straight-line code**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0029-run-the-run-rpc-as-straight-line-code.md#decision-outcome
  Governs `cmds/dutagent/run.go`, `internal/dutagent/locker`. `run` receives the command, finds it
  and resolves its arguments before it takes the device, then runs the session and the modules,
  returning early on failure. Each release is deferred right after its acquisition; modules run on
  the handler's goroutine through `catchPanic`. Access check and hold are one locker call; no state
  machine.
- **ADR-0031 Keep a device busy until its modules return**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0031-keep-a-device-busy-until-its-modules-return.md#decision-outcome
  Governs `cmds/dutagent/run.go`, `internal/dutagent/locker`, `internal/dutagent/session`,
  `pkg/module`. The handler returns, and the run's lease ends, only after the modules and the
  broker's workers have stopped. No timer cuts the wait short; while a cancelled module has not
  returned, the agent warns every 10 seconds. `unlock force` releases a reservation, never a
  running command's hold; on a device that only runs a command, it fails with FailedPrecondition.
- **ADR-0032 Stop the agent in stages**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0032-stop-the-agent-in-stages.md#decision-outcome
  Governs `cmds/dutagent/shutdown.go`, `cmds/dutagent/dutagent.go`, `cmds/dutagent/lifecycle.go`,
  `internal/dutagent/locker`. SIGTERM, SIGINT and SIGHUP count together; SIGQUIT is not caught.
  Stage 1 drains without a time limit, stage 2 cancels the commands (Aborted) and waits for the
  modules without a time limit, stage 3 exits at once and skips `Deinit`. A failure of the agent
  itself enters stage 2 directly. Work refused while stopping gets FailedPrecondition. `Deinit`
  runs only after stage 1 or 2, bounded to 15 seconds. No service manager is needed.
- **ADR-0036 Run a user's different commands side by side**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0036-run-a-users-different-commands-side-by-side.md#decision-outcome
  Governs `internal/dutagent/locker`, `cmds/dutagent/run.go`, `pkg/module`. The agent refuses a
  run when another owner reserved or uses the device, when it is stopping (draining still admits
  runs on a device the caller reserved), or when the same command, whatever its arguments, still
  runs or stops. Every other run is admitted. Each run holds the device through a lease of its own
  that only it ends, once its modules have returned; a forced unlock, the end of a reservation and
  the abort stage leave leases alone. Runs of other module instances, also of the same type,
  overlap, so what they share beyond their own fields, such as the process-wide GPIO register
  mapping, is serialized in code. Hardware that commands share, such as a serial port, a GPIO pin,
  a PDU outlet or a BMC, is the admin's concern; the agent does not check it.

## Logging and output

- **ADR-0019 Keep command output on stdout and diagnostics on stderr**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0019-keep-command-output-on-stdout-and-diagnostics-on-stderr.md#decision-outcome
  Governs `cmds/dutctl`, `internal/output`, `internal/style`. stdout carries only results and
  module output, rendered by the formatter (and, by ADR-0035, a RAW console's bytes while the
  terminal is raw); diagnostics go through `slog` to stderr, with only `slog.Debug` and
  `slog.Warn`. An error that ends the command is returned, not logged. Color only on a terminal.
- **ADR-0022 Log through a scoped logger in the context**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0022-log-through-a-scoped-logger-in-the-context.md#decision-outcome
  Governs `cmds/dutagent`, `cmds/exp/dutserver`, the agent's and server's packages under
  `internal` (not the client's `internal/output` and `internal/style`, which follow ADR-0019),
  `pkg/module`. Log through `log.FromContext(ctx)`; only bootstrap uses `slog` directly, and
  objects without a context keep a logger from construction. Scope and shared attributes are set
  at a component boundary. Logs go to stderr. Levels have fixed meanings; an error is logged once;
  modules log what the operator needs, never lifecycle markers.

## Repository, CI and releases

- **ADR-0008 Release from Conventional Commits with release-please**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0008-release-from-conventional-commits-with-release-please.md#decision-outcome
  Governs `.commitlintrc.yml`, `release-please-config.json`, `.github/workflows/release.yml`.
  Commit types as in CONTRIBUTING.md, no scopes; a breaking change has `!` and a
  `BREAKING CHANGE:` footer. commitlint checks the header of every commit.
- **ADR-0011 Build and test every commit of a pull request**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0011-build-and-test-every-commit-of-a-pull-request.md#decision-outcome
  Governs `.github/workflows/go.yml`. Every commit of a pull request builds for amd64 and arm64 and
  passes its tests with the race detector on its own; `All-Checks` gates the merge. CI checks each
  commit; a workflow change that stops checking every commit contradicts this ADR.
- **ADR-0013 Release v1.0.0 as alpha pre-releases**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0013-release-v1-0-0-as-alpha-pre-releases.md#decision-outcome
  Governs `release-please-config.json`, `.github/workflows/release.yml`. Every release after
  0.10.0 is a pre-release of 1.0.0; breaking changes ship in the next pre-release and its notes
  and do not raise the major version.
- **ADR-0020 Test dutctl on real hardware in FirmwareCI**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0020-test-dutctl-on-real-hardware-in-firmwareci.md#decision-outcome
  Governs `.firmwareci/`, `.github/workflows/hardware-integration*.yml`, `packaging/`. Hardware
  tests install snapshot packages; `packaging/` is an example built for them, and dutagent must
  not depend on a service manager. The fork workflow never checks out or runs the contributor's
  code, treats its artifact as untrusted, waits for the `firmwareci-contrib` approval, and is
  kept in line with the main hardware workflow by hand.
- **ADR-0038 Pre-review pull requests with Copilot**
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0038-pre-review-pull-requests-with-copilot.md#decision-outcome
  Governs `.github/copilot-instructions.md`, `.github/instructions/`, `.github/skills/code-review/`,
  `.github/CODEOWNERS`. Copilot's comments are hints, no required check; `All-Checks` and the
  maintainers' review stay the merge criterion. Each rule summarizes a convention that a document
  states, links it, and keeps an ID that is never used again; a pull request that changes a
  convention changes its rule too. Rules leave out what CI and the linters check and what concerns
  single commits.

## Superseded: never cite as current

- ADR-0006 Drive the Run handler with a generic state machine: superseded by ADR-0029.
- ADR-0012 Make the CLIs, config and RPC stable with v1.0.0: superseded by ADR-0028.
- ADR-0030 Run one command at a time per device: superseded by ADR-0036.
