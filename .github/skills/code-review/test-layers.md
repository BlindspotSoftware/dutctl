# Which change needs which test

dutctl is tested in three layers. Unit tests and integration tests run for every commit of a pull
request, under the race detector on amd64 and arm64; hardware tests run for the pull request's
result. A test goes into the lowest layer that can show the behavior.

| Change | Unit test | Integration test (`test/integration`) | Hardware test (`.firmwareci/`) |
|---|---|---|---|
| Logic inside a package: parsing, framing, error mapping, output formats | next to the code | — | — |
| Configuration grammar | `pkg/dut`, with `testdata/valid_*.yaml` and `invalid_*.yaml` | `TestConfig…` | — |
| A module's logic | `pkg/module/<name>`; a test that needs a Session uses `internal/test/mock.Session` | — | — |
| A module that needs no hardware (shell, file, time-wait, agent-status) | as above | `TestModules…` | — |
| The serial module's byte path | as above | `TestSerial…`, over a `socat` pseudo-terminal pair | Serial-Console |
| A module that drives the tester's hardware (GPIO, PDU, SPI flash) | as above | — | one test per hardware feature |
| The `module.Session` contract, the session broker, console framing | `internal/dutagent/session`, `cmds/dutagent/run_test.go` | `TestSession…`, through the demo modules; a new or changed Session feature also gets a demo module use and a row in `pkg/module/demo/README.md` | — |
| RPC handlers, locks and reservations, side-by-side runs | `cmds/dutagent`, `internal/rpc`, `internal/dutagent/locker` | `TestRPC…` | — |
| The version check between client and agent | `internal/rpc`, `internal/compat` | — (the harness builds both binaries at one version) | — |
| Client flags, terminal and raw mode, Ctrl-C and the escape key | `cmds/dutctl`, with `internal/test/pty` for a terminal | flags through `dutctl` (`TestRPC…`); terminal, raw mode and keys through `startOnTerminal` | — |
| Packaging, installing on the tester, a client reaching the agent over the network | — | — | the hardware workflow |
| A bug fix, also of a race, a hang or a lifetime bug | a test of the fix in the lowest layer that shows it | | |

Every agent configuration under `cmds/exp/contrib`, `pkg/module` and `test` is loaded by the unit
test `TestRepositoryConfigs`; an example configuration needs no test of its own.

Sources:

- https://github.com/BlindspotSoftware/dutctl/blob/main/.firmwareci/README.md#only-what-needs-the-hardware
- https://github.com/BlindspotSoftware/dutctl/blob/main/test/integration/harness_test.go
- https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0011-build-and-test-every-commit-of-a-pull-request.md#more-information
- https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0020-test-dutctl-on-real-hardware-in-firmwareci.md#more-information
- https://github.com/BlindspotSoftware/dutctl/blob/main/pkg/module/demo/README.md#what-the-modules-show
