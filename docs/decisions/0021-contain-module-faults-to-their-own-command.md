---
status: accepted
date: 2026-07-01
---

# Contain module faults to their own command

## Context and problem statement

One dutagent serves every DUT wired to its host, and a failed `Init` of any
module aborts the agent's start. Until July 2026, a fault in one module could
take the agent down for all of these devices:

- The ssh module test-dialed its DUT in `Init`. When the DUT was off or
  unreachable, `Init` failed and the agent did not start for any device. The
  ipmi module opened its BMC session in `Init`, with the same effect.
- A panic in a module's `Run` ended the agent and every run in flight,
  because the modules ran on a goroutine without a recover.

Since March 2026 the agent recovered a panic in a module's `Init` or `Deinit`,
so that one faulty module could not stop the others from initializing or
cleaning up. The serial module had opened its port per run since January
2026, so the agent started while the target was off.

## Considered options

- **Check the connection in `Init`**: test-dial the DUT or open the BMC
  session when the agent starts.
- **Connect when a command runs**: `Init` checks the configuration only, and
  the module connects per run or on first use.

## Decision outcome

Chosen option: **connect when a command runs**, because one unreachable DUT
must not keep the agent from serving the other devices, and a command has to
connect anyway.

Together with the recovery of module panics, this makes a fault in a module
fail only its own command:

- `Init` validates the configuration and what the module needs on the agent,
  such as a tool on the `PATH` or a key file. It does not reach out to the DUT
  or its BMC: no dial, no serial port, no BMC session. A module connects per
  run, as ssh and serial do, or on first use, as ipmi does. Setting up
  hardware on the agent's own host is fine: the GPIO modules drive their pins
  to an idle or initial level in `Init`.
- The agent recovers a panic in a module's `Init`, `Run` or `Deinit` at the
  framework boundary and turns it into the error of that call. `Init` and
  `Deinit` go on with the other modules. A panicking `Run` fails its command
  with `Aborted` and frees the device.
- Modules still return errors rather than panic; the recovery is a safety
  net.

### Consequences

- Good, because a DUT that is off or unreachable fails only the commands that
  need it, and the agent serves the other devices.
- Good, because a module bug fails one command, with "module panicked" in its
  error, instead of every run on the host.
- Bad, because a wrong host or port shows only at the first command, not at
  start or with `-dry-run`.
- Bad, because the recovery covers only calls into a module. A panic in a
  goroutine that a module or the agent starts still ends the agent at once.
- Bad, because the module contract does not state the rule for `Init` yet:
  its doc in `pkg/module/module.go` still calls `Init` a good place to
  establish connections.
- Neutral, because a panic in `Init` still fails the start, as any failed
  `Init` does ([ADR-0009](0009-initialize-every-module-once-at-agent-start.md)).

### Confirmation

Reviewers check that the `Init` of a new module does not contact its device;
a test in [pkg/module/serial](../../pkg/module/serial) initializes the serial
module with a device that does not exist. Tests in
[cmds/dutagent](../../cmds/dutagent) cover a panicking `Init` and a panicking
`Run`.

## More information

- [ADR-0004](0004-compile-modules-into-the-agent-behind-a-session.md) and
  [ADR-0009](0009-initialize-every-module-once-at-agent-start.md): the module contract
  and when `Init` and `Deinit` run.
- [ADR-0025](0025-map-errors-to-connect-codes-only-in-rpc-handlers.md): how a module's error
  reaches the client.
- [ADR-0027](0027-cancel-work-through-contexts-without-a-deadline-on-run.md): how a module is stopped.
- [docs/dutagent-shutdown.md](../dutagent-shutdown.md) lists what a user sees
  when a module fails or panics.
- Code: [cmds/dutagent/lifecycle.go](../../cmds/dutagent/lifecycle.go),
  [pkg/module/module.go](../../pkg/module/module.go).
- Later changes:
  - 2026-07: the agent also recovered panics in a module's `Run`. The ipmi
    module stopped opening its BMC session in `Init`; it connected on first
    use and, when a command failed, reconnected and retried it once.
  - 2026-10: `Init`, `Run` and `Deinit` all went through one `catchPanic`,
    when the Run handler became straight-line code
    ([ADR-0029](0029-run-the-run-rpc-as-straight-line-code.md)).

Recorded retroactively in October 2026.
