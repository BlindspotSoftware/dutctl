---
status: accepted
date: 2024-09-18
---

# Release from Conventional Commits with release-please

## Context and problem statement

Since April 2024, commitlint has checked in CI that the commit messages of
dutctl follow Conventional Commits: each subject starts with a type such as
`feat` or `fix`. In September 2024 the project prepared its first release. Every
release needs a version number, release notes and binaries to download.

The commit messages already say whether a change adds a feature, fixes a bug
or breaks compatibility, which is what a version bump and a changelog need. The
question was how to turn that history into releases.

## Decision outcome

Commit messages follow Conventional Commits, and release-please turns them into
versions, a changelog and GitHub releases.

- The type is one of `build`, `chore`, `ci`, `docs`, `feat`, `fix`,
  `refactor`, `revert` and `test`. Scopes are not used.
- A breaking change has a `!` after its type and a `BREAKING CHANGE:` footer
  that says what breaks.
- On every push to main, release-please updates a release pull request with the
  next version and the changelog. Its title and commit read
  `chore: release <version>`, a valid Conventional Commit.
- release-please computes the version from the commit types since the last
  release. While the major version is 0, a breaking change raises the minor
  version (`bump-minor-pre-major`).
- The changelog has sections for `feat`, `fix` and `docs`, and lists breaking
  changes on their own. `refactor`, `test`, `build` and `chore` go to Other
  Work; `ci` and `revert` are left out.
- Merging the release pull request tags the release as `vX.Y.Z` and publishes
  it on GitHub. The release workflow then builds the Linux binaries with
  `go build` and attaches them.
- release-please runs with a personal access token: pull requests opened with
  the workflow's default token do not start other workflows, so commitlint
  would not run on the release pull request.
- The first release was 0.1.0. release-please had proposed 1.0.0; those
  release pull requests were closed, and an empty commit with the footer
  `Release-As: 0.1.0` set the version.

### Consequences

- Good, because versions and release notes follow from the history; cutting a
  release means merging the release pull request.
- Good, because breaking changes stand out in the changelog.
- Bad, because commit subjects become the release notes as they are written,
  and a wrong type files a change under the wrong heading or misses a version
  bump.
- Bad, because releases depend on a personal access token kept as a repository
  secret.

### Confirmation

The "Conventional Commits" job in
[.github/workflows/repo.yml](../../.github/workflows/repo.yml) runs commitlint
with [.commitlintrc.yml](../../.commitlintrc.yml) on every commit of a pull
request and on the last commit pushed to main.

## More information

- [ADR-0011](0011-build-and-test-every-commit-of-a-pull-request.md): CI also builds and
  tests every commit of a pull request.
- [ADR-0013](0013-release-v1-0-0-as-alpha-pre-releases.md): the 1.0.0 alpha
  releases that followed 0.10.0.
- [CONTRIBUTING.md](../../CONTRIBUTING.md#conventional-commits): the commit
  types for contributors.
- [release-please-config.json](../../release-please-config.json) and
  [.github/workflows/release.yml](../../.github/workflows/release.yml).
- [Conventional Commits](https://www.conventionalcommits.org/) and
  [release-please](https://github.com/googleapis/release-please).
- Later changes:
  - 2025-07: `chore` commits moved to Other Work, so that dependency updates
    showed in the release notes.
  - 2026-05: release-please started to write each new version into
    `internal/buildinfo/version.go` and the example configurations.

Recorded retroactively in October 2026.
