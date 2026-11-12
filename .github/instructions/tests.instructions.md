---
applyTo: "**/*_test.go,test/**,internal/test/**,.firmwareci/**,.github/workflows/go.yml,.github/workflows/hardware-integration*.yml"
---

# Review rules for tests

dutctl is tested in three layers: unit tests next to the code; integration tests in
`test/integration`, which run the real `dutctl` and `dutagent` against each other on the CI
runner; and hardware tests in FirmwareCI (`.firmwareci/`), which run on a tester with real
hardware. CI runs the unit and integration tests for every commit of a pull request, under the
race detector on amd64 and arm64, and the hardware tests for the pull request's result. Which
layer a test belongs in is GEN-6; the `code-review` skill has the table.

## Integration tests (`test/integration`)

- [TEST-1] Every test calls `build(t)` before it starts a process, directly or through
  `startAgent`, `startAgentYAML` or `startSerialLine`, so that it skips unless
  `DUTCTL_INTEGRATION=1` is set, and with `-short`. A test that needs a running agent starts one
  of its own with `startAgent` or `startAgentYAML`, which listen on `127.0.0.1:0`; never a fixed
  port, never an agent shared between tests. A test of an agent that must refuse to start runs
  `binaries.agent` itself, on port 0.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/test/integration/harness_test.go
- [TEST-2] Tests wait for what the processes write, through the harness's waits (`waitFor`,
  `waitMatch`, `waitRecord`, `wait`), which are bounded by `waitTimeout`. No `time.Sleep` and no
  polling loops: a flaky test blocks unrelated pull requests, because every commit runs the tests.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/test/integration/harness_test.go
- [TEST-3] Tests run `dutctl` only through the harness: the `agent` methods `dutctl`,
  `dutctlInput`, `start` and `startOnTerminal`, or `newClientCmd`, passed to `start`, for a call
  without an agent. `newClientCmd` gives the client a home of its own and logs each call as a user
  would type it; never `exec.Command(binaries.client, …)` in a test.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/test/integration/harness_test.go
- [TEST-4] A test's name starts with its area: `TestRPC`, `TestSession`, `TestConfig`,
  `TestModules` or `TestSerial`. Every file's `//go:build` constraint includes `linux`. A test
  that needs `socat` or a pseudo-terminal skips when it is missing instead of failing.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/test/integration/harness_test.go

## Unit tests and test doubles

- [TEST-5] Modules are unit-tested against `internal/test/mock.Session`. Its panics on unset
  fields are deliberate; do not suggest removing them. Test doubles stay small and model only what
  a test needs.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/internal/test/README.md#guiding-principles
- [TEST-6] Tests that need a terminal use `internal/test/pty` instead of opening `/dev/ptmx`
  themselves.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/internal/test/README.md
- [TEST-7] The linter does not check test files, so check the idioms of its enabled test linters
  here: helpers call `t.Helper()`, and per-test state uses `t.TempDir()` and `t.Setenv()` instead
  of `os.MkdirTemp` and `os.Setenv`.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/.golangci.yml

## Hardware tests (`.firmwareci/`)

- [TEST-8] Each hardware feature gets a test file of its own, and the test's `name` says which
  feature it tests, so a check reads like "Hardware-Integration - GPIO-Power". A test refers to
  the device through `[[attributes.*]]` from the DUT's `dut.yaml`, never through a hard-coded host
  or port, and the commands it uses go into `test/test-dutagent-cfg.yaml`. Setup and tear-down
  stay in the DUT's `pre.yaml` and `post.yaml` unless a test needs something different.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/.firmwareci/README.md#adding-a-new-test

## Workflows

- [TEST-9] `hardware-integration-fork.yml` checks out only the base repository and never runs the
  contributor's code. It treats the artifact of the contributor's run as untrusted and waits for a
  maintainer to approve the `firmwareci-contrib` environment. Its submission step repeats the one
  in `hardware-integration.yml` and is kept in line with it by hand, so a change to one, such as
  the FirmwareCI action's version or the files it passes, changes the other in the same pull
  request.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0020-test-dutctl-on-real-hardware-in-firmwareci.md#decision-outcome
- [TEST-10] A new job that builds or tests the commits of a pull request goes into the `needs` of
  `all-checks` in `go.yml`: the per-commit checks change their names with every pull request, so
  only All-Checks can be required, and a job left out of it does not block a merge. The lint jobs
  stay outside All-Checks for now.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/0011-build-and-test-every-commit-of-a-pull-request.md#more-information

## Do not flag

- Bounded waits in hardware tests, such as a PDU settling between two switch commands: real
  hardware needs them (ADR-0020).
- A test that leaves a reservation behind in `test/integration`: the harness stops the agent
  regardless.
- The lint jobs missing from `all-checks` (TEST-10).
