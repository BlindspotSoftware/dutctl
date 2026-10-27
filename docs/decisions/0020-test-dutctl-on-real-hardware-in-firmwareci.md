---
status: accepted
date: 2026-06-30
---

# Test dutctl on real hardware in FirmwareCI

## Context and problem statement

Until June 2026, the only tests in CI were the Go unit tests. No test installed
dutagent on the kind of single-board computer it is meant for, and no test
drove such an agent over the network with a real dutctl.

FirmwareCI is a service that runs test workflows on physical devices. A pull
request proposed a FirmwareCI test for dutctl, with requirements that are kept
in [.firmwareci/README.md](../../.firmwareci/README.md): tests that are
parametric, reusable, easy to extend and reliable, and that can check every
pull request. The question was how such a test fits into the repository and
its CI.

## Decision outcome

dutctl is tested end to end on real hardware through FirmwareCI, for pull
requests and for pushes to main.

- The `fwci-test` workflow builds snapshot packages with GoReleaser and nFPM.
  GoReleaser serves only as a build tool; releases still come from
  release-please ([ADR-0008](0008-release-from-conventional-commits-with-release-please.md)).
- FirmwareCI receives the arm64 Debian package, the amd64 dutctl binary, an
  agent configuration and files that single tests need. It installs the
  package on a Raspberry Pi tester, starts dutagent there, runs dutctl against
  it over the network and checks the output and the effect on the hardware
  wired to the tester.
- The tester installs a package instead of copying binaries: uninstalling
  removes every file of the tested version, and the package manager brings the
  runtime dependencies.
- The test definitions live in this repository under `.firmwareci/`, not in a
  separate repository pulled in as a git submodule. The maintainers wanted no
  trade-offs for compatibility with other projects.
- The packages carry the example packaging in `packaging/`, a systemd unit and
  its helper files, which the tester uses to start dutagent. This packaging is
  an example built for the test: releases attach plain binaries, and dutagent
  does not depend on a service manager.
- CI also runs the built dutagent with `-check-config` on the example
  configurations and the test configuration in the repository.
- Pull requests from forks get no repository secrets, so `fwci-contrib` submits
  their build from the base repository. It never checks out or runs the
  contributor's code, and it treats the downloaded artifact as untrusted. A
  maintainer must approve the `firmwareci-contrib` environment first, because
  approving lets the contributor's build run on real hardware.

### Consequences

- Good, because each pull request shows whether its packages install, and
  whether dutctl and dutagent work together on the hardware that dutagent is
  meant for.
- Good, because an example configuration that no longer passes `-check-config`
  fails CI, so the examples follow changes of the configuration grammar.
- Bad, because the hardware check depends on one tester and on an external
  service; when either has a problem, the check fails for reasons outside the
  code.
- Bad, because real hardware fails in ways that unit tests do not, for example
  a PDU that has not settled between two switch commands; such tests need
  waits.
- Bad, because the hardware tests of a fork's pull request wait for a
  maintainer, who should read the diff before approving.
- Bad, because `fwci-contrib` repeats the submission step of `fwci-test` and
  has to be kept in line with it by hand.
- Neutral, because GoReleaser is a second build path next to the plain
  `go build` of the release workflow.

## More information

- [ADR-0011](0011-build-and-test-every-commit-of-a-pull-request.md): the unit tests,
  which run on every commit of a pull request.
- [.firmwareci/README.md](../../.firmwareci/README.md): the layout of the tests
  and how to add one.
- [packaging/README.md](../../packaging/README.md): the example packaging.
- [.github/workflows/fwci-test.yml](../../.github/workflows/fwci-test.yml)
  builds the packages and runs the hardware test and the configuration check.
- [.github/workflows/fwci-contrib.yml](../../.github/workflows/fwci-contrib.yml)
  runs the hardware test for pull requests from forks.
- Later changes:
  - 2026-07: CI started to validate the configurations with the built
    dutagent. The example systemd unit moved from `.firmwareci/` to
    `packaging/`.
  - 2026-08: pull requests from forks started to reach FirmwareCI through
    `fwci-contrib`, behind the reviewer gate.
  - 2026-09: one combined test of the dummy modules gave way to separate test
    files for nine modules, several of them against hardware wired to the
    tester, so a failure names the module that broke. The shared setup moved
    from Jinja templates, whose output had to be committed, to the tester's pre
    and post stages.
  - 2026-09: `dutagent -check-config` started to exit non-zero for an invalid
    configuration; until then the validation caught only crashes.

Recorded retroactively in October 2026.
