---
status: accepted
date: 2026-06-29
---

# Keep command output on stdout and diagnostics on stderr

## Context and problem statement

Since August 2025 dutctl rendered its results through an output formatter that
writes text, JSON, YAML or one line per entry, chosen with `-f`. The client's
own diagnostics bypassed it: the standard logger and the final error went to
stdout, between the command output. That broke `-f json` and `-f yaml`, mixed
log lines into piped output, and a line such as "Receive routine terminating:
Stream closed by agent" ended every run.

The maintainers asked for a client that forwards what the agent produces and
logs nothing of its own apart from connection errors, since the agent does the
work and logs it.

## Decision drivers

- stdout stays parseable for `-f json`, `-f yaml` and pipes.
- By default, diagnostics do not interrupt streaming output from a module.
- A user can ask for more detail while debugging, or for none in scripts.

## Decision outcome

dutctl keeps stdout for what the command produced and writes everything about
the client itself to stderr.

- stdout carries results and module output, rendered by the formatter in the
  format chosen with `-f`. Error content, such as a module's stderr stream, goes
  to stderr.
- Diagnostics go through `log/slog` to a handler that writes to stderr only.
  `--log` selects `debug`, `warn` (the default) or `none`.
- There are two levels. `slog.Debug` is internal trace, shown only with
  `--log debug`. `slog.Warn` marks a non-fatal anomaly; at the default level
  warnings are collected and printed as a summary when the command ends. With
  `--log debug` every record is printed as it happens.
- An error that ends the command is returned, not logged. It reaches a single
  exit point, which renders it through the formatter on stderr and exits with
  1, or with 130 after an interrupt. There is no error log level.
- Color is used only on a terminal: it is off when output is piped or
  redirected, and with `-no-color`.

### Consequences

- Good, because a program reading stdout gets only what the formatter renders,
  in the chosen format.
- Good, because warnings do not tear up streaming output; they appear once,
  after it.
- Good, because a user can turn diagnostics up or off without touching the
  command output.
- Bad, because at the default level a warning shows up only when the command
  ends, which in a long run can be long after its cause.
- Bad, because the exit code does not tell failures apart: every error that
  reaches the exit point exits with 1.

### Confirmation

- forbidigo in [.golangci.yml](../../.golangci.yml) rejects `fmt.Print` and
  its variants, which write to stdout directly, and allows only `slog.Debug`
  and `slog.Warn` among the slog log calls in the client.
- [cmds/dutctl/clilog_test.go](../../cmds/dutctl/clilog_test.go) tests the
  handler in each `--log` mode.
- The Logging section of [CONTRIBUTING.md](../../CONTRIBUTING.md#logging) states
  the rules for contributors.

## More information

- [ADR-0022](0022-log-through-a-scoped-logger-in-the-context.md): how the agent and
  the server log.
- [cmds/dutctl/clilog.go](../../cmds/dutctl/clilog.go): the handler and
  `--log`; [cmds/dutctl/dutctl.go](../../cmds/dutctl/dutctl.go): the exit
  point; [internal/output](../../internal/output): the formatters.

Recorded retroactively in October 2026.
