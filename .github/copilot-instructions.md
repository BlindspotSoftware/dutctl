# Review rules for dutctl

dutctl is written in Go. The agent `dutagent` drives devices under test through modules
(`pkg/module`), and the client `dutctl` talks to it over Connect/gRPC (`protobuf/dutctl/v1`).
The project's conventions are written down in CONTRIBUTING.md, in `docs/`, in READMEs, in doc and
code comments and in the decision records (ADRs) in `docs/decisions/`. Each rule below summarizes
one of them. Where a rule and its source disagree, the source wins.

When a change breaks a rule, name the rule's ID and give its link, for example
"[GEN-3] … → https://…". Comment only on what the pull request changes. Do not repeat what CI
and the linters already check: commit message format, the file checks on decision records,
license headers, the build and race-free tests of every commit, and where `slog` may be called.

More rules apply by path, from `.github/instructions/`. The `code-review` skill in
`.github/skills/code-review/` lists the constraint of every ADR and which test layer a change
needs.

## Decisions and public interfaces

- [GEN-1] Ask for a new ADR when a change alters a primary public interface (the agent
  configuration grammar, the RPC package `dutctl.v1`, the module API in `pkg/module`), a rule
  every module must follow, or how runs, devices, locks, cancellation or shutdown behave; when it
  sets a convention for the whole code base, such as for logging, errors or tests; when it changes
  how the repository builds, tests or releases dutctl; or when it adds a dependency or tool that
  would be hard to replace. Features that follow existing patterns, bug fixes, and refactorings
  that keep every contract need none.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/README.md#when-to-write-an-adr
- [GEN-2] A change must not contradict an accepted ADR; to reverse a decision, the pull request
  adds a new ADR that supersedes the old one. ADR-0006, ADR-0012 and ADR-0030 are superseded by
  ADR-0029, ADR-0028 and ADR-0036: never cite them as current.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/README.md#changing-a-decision
- [GEN-3] A change users must adapt to is breaking: a renamed or removed configuration key,
  module ID or module option, a changed default or meaning of one, a removed, renamed or
  renumbered field or RPC in `protobuf/dutctl/v1`, a new method on `module.Module` or
  `module.Session`, a changed exported signature in `pkg/module`, behavior that a doc comment in
  `pkg/module` promises, a changed command-line flag or default, a new reserved word in the device
  or command position in `internal/keyword`, a renamed header in `pkg/headers`. Say that the
  change is breaking and what users must do; its commit needs `!` after the type and a
  `BREAKING CHANGE:` footer. The command-line interfaces are best effort: a change to them needs
  no ADR unless it reverses one, such as the positional grammar of ADR-0010.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/CONTRIBUTING.md#breaking-changes
- [GEN-4] A change to a `.proto` file brings the regenerated code under `protobuf/gen` (buf lint,
  then buf generate) in the same change; nobody edits `protobuf/gen` by hand. No CI job runs buf.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/protobuf/README.md#code-generation
- [GEN-5] dutctl is a hardware-control layer: named commands per device, their discovery and one
  lock per device. Flag schedulers, job queues, test frameworks, idle power policies and firmware
  image stores; expose information through the RPC API instead.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0024-keep-dutctl-a-hardware-control-layer.md#decision-outcome

## Tests

- [GEN-6] New behavior and bug fixes come with tests in the same change, in the lowest layer that
  can show the behavior: a unit test next to the code; a test in `test/integration` when it takes
  the real `dutctl` and `dutagent` (the RPCs, the Session contract, the configuration grammar, the
  client's flags and terminal, modules that need no hardware); a FirmwareCI test in `.firmwareci/`
  only when it takes the tester's hardware (GPIO, PDU, SPI flash, serial port) or the installed
  package. dutctl's own corner cases never go to FirmwareCI. The `code-review` skill has the table
  of which change needs which layer.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/.firmwareci/README.md#only-what-needs-the-hardware

## Errors

- [GEN-7] A package returns its own sentinels and error types, wrapped with context (`%w`), and
  its doc comments say which ones callers may match with `errors.Is` or `errors.As`. Third-party
  error types are not part of that contract: `pkg/dut`, for example, turns validator errors into
  its own `ErrModuleValidation`. A module's error reaches the client only as text.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0025-map-errors-to-connect-codes-only-in-rpc-handlers.md#decision-outcome
- [GEN-8] Only the RPC layer assigns connect codes: the dutagent handlers and their helpers in
  `cmds/dutagent` (`rpc.go`, `run.go` and the mapping helpers in `errmap.go`), the dutserver
  handlers, and the version interceptor in `internal/rpc`. Every dutagent handler that can fail
  lists its codes in an `Errors:` paragraph of its doc comment; check that the paragraph still
  matches the code.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0025-map-errors-to-connect-codes-only-in-rpc-handlers.md#decision-outcome
- [GEN-9] Log an error once, where it becomes terminal; on the agent that is the RPC handler.
  Layers in between return it without logging. A lower layer may log one detail line in its own
  scope when the handler's summary would lose it, and a comment marks that line as deliberate.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0025-map-errors-to-connect-codes-only-in-rpc-handlers.md#decision-outcome

## Logging and output

- [GEN-10] In the client (`cmds/dutctl`, `internal/output`), stdout carries only command output:
  what the output formatter renders and, while a RAW console has the terminal in raw mode, the
  module's bytes unchanged. Diagnostics, usage and errors go to stderr. Log only with
  `slog.Debug` and `slog.Warn`. An error that ends the command is returned, not logged. The linter
  misses `fmt.Fprint(os.Stdout, …)`, `slog.Default().Info(…)` and `slog.With(…).Error(…)`.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0019-keep-command-output-on-stdout-and-diagnostics-on-stderr.md#decision-outcome
- [GEN-11] On the agent and the server, log to stderr through `log.FromContext(ctx)`. Without a
  request context, process bootstrap logs through `slog` directly, and objects whose methods take
  no context (the session, the locker, `chanio`) use a logger set at construction. At a component
  boundary the caller sets the scope (`log.WithScope`) and shared attributes (`log.With`) on the
  context before it hands control on; a line in a component's scope is logged by that component.
  No component prefixes in message text. A file added to the setup exclusions of the `forbidigo`
  logging rules in `.golangci.yml` must have no request context to log from.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0022-log-through-a-scoped-logger-in-the-context.md#decision-outcome
- [GEN-12] Levels on the agent and the server: Error when the agent failed at what it was asked to
  do and an operator likely must act; Warn for a handled or tolerated anomaly, which includes a
  request cancelled by its client or by a stop and a request refused for what it asked or for the
  device's state (see `logRunSummary` in `cmds/dutagent/rpc.go`); Info for a few milestones per
  request; Debug for internal tracing. Flag Info or higher on a per-message path.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0022-log-through-a-scoped-logger-in-the-context.md#more-information

## Context

- [GEN-13] Pass the context on through every layer, and let every blocking call take it or select
  on `ctx.Done()`. No context-ignoring `time.Sleep` where work must stop on cancellation. The Run
  RPC gets no deadline: no timeout on the client's Run call or on a run's context in the agent, no
  `http.Client.Timeout` or `ResponseHeaderTimeout` on the RPC client in `internal/rpc`, no
  `ReadTimeout` or `WriteTimeout` on the agent's server. Allowed are the transport's dial and idle
  timeouts, the server's `ReadHeaderTimeout`, and the deadlines of the unary RPCs.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0027-cancel-work-through-contexts-without-a-deadline-on-run.md#decision-outcome

## Documentation

- [GEN-14] A change updates, in the same pull request, the documentation that describes the
  behavior it changes: doc comments, README.md, `docs/` (`dutagent-config.md` for the
  configuration grammar, `module_guide.md` for the module API), and a module's README and example
  configuration. Exported symbols have doc comments.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/CONTRIBUTING.md#documentation-commits
- [GEN-15] A change to a convention, in an ADR, CONTRIBUTING.md, `docs/` or a doc comment in
  `pkg/module`, also changes the rule that summarizes it here, in `.github/instructions/` or in
  `.github/skills/code-review/`, in the same pull request; a new ADR gets its entry in the skill's
  `decisions.md`. A new rule links a document that states it. A rule keeps its ID, and the ID of a
  removed rule is not used again.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/CONTRIBUTING.md#automated-review

## Do not flag

- `%v` for a module's error in `moduleError` in `cmds/dutagent/errmap.go`: a module's error
  reaches the client as text on purpose (ADR-0025).
- The year 2025 in license headers of new files: every file carries it, and CI matches the text.
- Runs of one user's different commands on one device at the same time: they are allowed
  (ADR-0036); only the same command twice is refused.
- A timeout a module sets on its own device I/O, such as the serial module's `-t`: GEN-13 covers
  only the Run RPC.
