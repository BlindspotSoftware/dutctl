---
status: accepted
date: 2026-07-05
---

# Log through a scoped logger in the context

## Context and problem statement

dutagent and dutserver logged with the standard `log` package, to stdout, with
no levels and no common structure. Some components put their own prefix in
front of a line, modules logged markers such as "Run called", and dutserver
printed every message it forwarded in full. Most lines did not say which
device, command or user they concerned.

The client had just moved its diagnostics to stderr and `log/slog`
([ADR-0019](0019-keep-command-output-on-stdout-and-diagnostics-on-stderr.md)). An issue from 2024 had
suggested slog as a backend, with modules still calling the standard `log`
package. The question was how the two daemons should log.

## Decision drivers

- Daemon logs need levels and a structured form that can be filtered.
- stdout is reserved for program output, such as the version banner.
- The context already reaches every component on the request path.
- The linter, not review, says which logging call is allowed where.

## Decision outcome

dutagent and dutserver log through `internal/log`, a thin layer on `log/slog`,
and take the logger from the context.

- The logger travels in the `context.Context`, and code gets it with
  `log.FromContext(ctx)`. Process bootstrap, which has no request context, logs
  through `slog` directly. Objects whose methods take no context, such as the
  locker and the session, keep a logger set at construction.
- A scope is one flat label that names a component, such as `rpc`, `session`,
  `locker` or `module`; a new scope replaces the old one. The scope and shared
  attributes, such as the device and command, are set on the context at a
  component boundary (`log.WithScope`, `log.With`), so each component logs only
  its own concern. In a run, for example, the agent announces each module in
  its own scope and hands the module a context in scope `module`.
- Logs go to stderr only. A text line reads
  `2006/01/02 15:04:05 LEVEL [scope] message key=value`, colored only on a
  terminal. `-log-json` switches to JSON, with the scope as an attribute, and
  `-log` sets the level: `debug`, `info`, `warn` or `error`.
- Error means that the agent failed at something it was asked to do and an
  operator likely must act. Warn is a handled or tolerated anomaly, Info marks
  a few milestones per request, and Debug is internal tracing.
- An error returned up the call stack is logged once, where it becomes
  terminal, not at every hop.
- Modules log for the operator who wrote the configuration: the tools, hosts
  and devices they talk to, with the parameters in effect, and the defaults
  they apply. They log no lifecycle markers; the framework does.
- golangci-lint enforces which calls are allowed where. forbidigo allows the
  logging functions of the `slog` package only in the client, which may use
  only Debug and Warn, and in the listed setup files; all other code,
  including any new file, takes the logger from the context. depguard bans the
  standard `log` package.

### Consequences

- Good, because every line names its component, and lines on the request path
  carry the attributes of their request without the code passing them on.
- Good, because JSON output can be filtered by scope and attribute.
- Bad, because the module contract changed: `Init` and `Deinit` take a context,
  so that modules get the logger there too.
- Bad, because a new setup file that has to log without a context must be
  added to the exclusions in `.golangci.yml`.
- Neutral, because the logs moved from stdout to stderr.

### Confirmation

golangci-lint runs in CI with the "slog logging buckets" rules for forbidigo
and the `no-std-log` rule for depguard in [.golangci.yml](../../.golangci.yml).
The tests of `internal/log` pin how scopes are rendered and replaced.

## More information

- [CONTRIBUTING.md](../../CONTRIBUTING.md#agent-and-server-logging): the
  logging rules for contributors.
- [internal/log](../../internal/log): the package and its documentation.
- [ADR-0019](0019-keep-command-output-on-stdout-and-diagnostics-on-stderr.md): the client's output and
  diagnostics.
- [ADR-0025](0025-map-errors-to-connect-codes-only-in-rpc-handlers.md): how errors travel up
  to the RPC handler, where they are logged.
- [ADR-0009](0009-initialize-every-module-once-at-agent-start.md): module `Init` and
  `Deinit`.
- Later changes:
  - 2026-09: the default level of dutagent and dutserver changed from debug to
    info.
  - 2026-10: when a command was cancelled, the module's error and the summary
    of the request became warnings: the error is the fallout of the
    cancellation, not a fault.

Recorded retroactively in October 2026.
