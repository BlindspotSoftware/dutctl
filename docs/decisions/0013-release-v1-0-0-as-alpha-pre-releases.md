---
status: accepted
date: 2025-07-27
---

# Release v1.0.0 as alpha pre-releases

## Context and problem statement

By July 2025, dutctl had released 0.1.0 to 0.10.0 with release-please
([ADR-0008](0008-release-from-conventional-commits-with-release-please.md)). A public launch of a
1.0 release had been planned since 2024. The README, rewritten for a
pre-release, named the public interfaces that would become stable and
backwards compatible with v1
([ADR-0012](0012-make-the-clis-config-and-rpc-stable-with-v1-0-0.md)).

The maintainers wanted real user feedback before they fix those interfaces. How
should releases be versioned until then?

## Considered options

- **Launch v1.0.0**: release the stable version and fix the public interfaces
  now.
- **Pre-release 1.0.0 as alphas**: release `1.0.0-alpha.N` versions while users
  try the interfaces.

## Decision outcome

Chosen option: **pre-release 1.0.0 as alphas**, because the public interfaces
should meet real users before v1.0.0 promises to keep them stable.

- After 0.10.0, every release is a pre-release of 1.0.0. The train starts
  with alphas, versioned `1.0.0-alpha.N` and tagged `v1.0.0-alpha.N`.
- Breaking changes ship in the next alpha and appear in its release notes; they
  do not raise the major version.
- The first alpha was set by hand: an empty commit carries the footer
  `Release-As: v1.0.0-alpha.1`. release-please's `prerelease` and
  `prerelease-type` settings had been tried and were removed the same day: at
  that time they only marked the GitHub release as a pre-release and did not
  change the version.

### Consequences

- Good, because the version shows that the line leads to v1.0.0 and is not
  stable yet; semantic versioning sorts every `1.0.0-alpha.N` before `1.0.0`.
- Bad, because release-please's default versioning did not continue the
  train: after the first alpha, its release pull request proposed
  2.0.0-alpha.1 instead of 1.0.0-alpha.2, so each further alpha would have
  needed its own `Release-As` footer.
- Bad, because Go treats the alphas as pre-releases: while v0.x release tags
  exist, `go install ...@latest` installs v0.10.0, not the newest alpha.

## More information

- [ADR-0008](0008-release-from-conventional-commits-with-release-please.md): how release-please
  derives versions and the changelog from the commits.
- [ADR-0028](0028-tier-the-public-interfaces-for-v1-0-0.md): which interfaces v1.0.0 makes
  stable, and the alpha and beta phases before it.
- [ADR-0023](0023-reject-rpcs-from-another-major-version.md): a client and an agent from
  different alphas differ only in the pre-release part, so the agent accepts
  the call and the client only warns.
- [README.md](../../README.md#release-plan): the release plan.
- [release-please-config.json](../../release-please-config.json) and
  [.github/workflows/release.yml](../../.github/workflows/release.yml).
- Later changes:
  - 2026-08: release-please started to compute the next alpha itself, with
    `versioning: prerelease`, `prerelease: true` and `prerelease-type: alpha`,
    which works since release-please v17. Graduating to 1.0.0 means setting
    `prerelease` to false.
  - 2026-08: the same `prerelease` setting marked each GitHub release as a
    pre-release, which would have kept GitHub's Latest release at v0.10.0.
    The release workflow now removes the mark and then marks the release as
    Latest.
  - 2026-08: the `go install` commands in the README were pinned to the exact
    release tag, which release-please updates with each release.

Recorded retroactively in October 2026.
