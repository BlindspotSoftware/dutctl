---
status: accepted
date: 2025-07-10
---

# Build and test every commit of a pull request

## Context and problem statement

Until July 2025, CI built and tested only the state that a pull request would
merge. The history of main is linear, without merge commits: the commits of a
pull request land on main one by one. An intermediate commit that does not
build, or fails its tests, then stays in that history and gets in the way of
`git bisect`.

Should CI check each commit of a pull request, at the price of more CI time?

## Decision drivers

- `git bisect` works on every commit of main.
- CI time stays acceptable.

## Considered options

- **Test every commit**: build and test each commit of a pull request, from the
  base branch to the head.
- **Test only the result**: build and test only the state that the pull request
  merges, as CI did so far.

## Decision outcome

Chosen option: **test every commit**, because it checks every commit that will
land on main, which keeps `git bisect` usable on all of them.

- For a pull request, the Go workflow lists every commit between the base
  branch and the head, and the merge result when it differs from the head.
- Each listed commit is built for amd64 and for arm64, and its tests run with
  the race detector (`go test -race`).
- Both legs of this matrix run on GitHub's amd64 runner, and only the build sets
  `GOARCH`. So the tests run on amd64 in both legs; the arm64 leg shows that the
  code compiles for arm64.
- A push to main tests only its head, to save CI time.

Each commit of a pull request therefore has to build and pass the tests on its
own: a pull request is a series of self-contained commits.

### Consequences

- Good, because a bug can be bisected on main without stepping over broken
  commits.
- Good, because each commit can be reviewed as a working state of the code.
- Bad, because CI time grows with the number of commits: each commit takes two
  build-and-test jobs.
- Bad, because a test that fails at random gets a chance to fail on every
  commit, so flaky tests hurt more than with one run per pull request.
- Bad, because the tests run twice on amd64 and never on arm64, the
  architecture that the second leg names.

## More information

- [ADR-0008](0008-release-from-conventional-commits-with-release-please.md): the commit message
  rules, which commitlint also checks on every commit.
- [ADR-0020](0020-test-dutctl-on-real-hardware-in-firmwareci.md): the tests on real
  hardware, which run on the result of a pull request, not per commit.
- [.github/workflows/go.yml](../../.github/workflows/go.yml): the jobs "Get
  Commits to Test" and "Build & Test".
- Later changes:
  - 2026-07: the tests began to run with the race detector.

Recorded retroactively in October 2026.
