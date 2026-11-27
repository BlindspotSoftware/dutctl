---
status: accepted
date: 2026-11-27
---

# Publish the documentation with Hugo and Relearn on GitHub Pages

## Context and problem statement

The documentation is Markdown that GitHub renders file by file: no navigation, no search, and no
way to arrange it by role and type as
[ADR-0041](0041-structure-the-documentation-by-diataxis-with-a-guide-per-role.md) asks. The files
must stay where they are and stay readable on GitHub: decision records, code, the systemd unit and
the review rules link them, and CI allows no other files in `docs/decisions/`. Program output, such
as `dutctl -h`, should appear in the documentation without a copy in the repository. Which tool
builds the site, and how does it get its pages?

## Decision drivers

- The Markdown files stay plain GitHub Markdown in their places, with relative links that work on
  GitHub.
- One more toolchain for a Go project should be small.
- Blindspot Software builds the documentation of FirmwareCI with Hugo and the Relearn theme.

## Considered options

- **Hugo with the Relearn theme**: a single Go binary; the theme as a Hugo module.
- **Material for MkDocs, or its successor Zensical**: Python; Material for MkDocs is in maintenance
  mode, Zensical is young and its plugin support unsettled.
- **Docusaurus or Starlight**: Node.js; Starlight requires front matter in every file.
- **GitHub's rendering only**: no site.

## Decision outcome

Chosen option: **Hugo with the Relearn theme**, because it adds a single binary, matches the
documentation of FirmwareCI, and its content adapters can build pages from files that stay as they
are.

- The site lives in `site/`, a Hugo module of its own that pins Relearn in `site/go.mod`. `task
  docs` builds it, and `task docs:serve` serves it locally. `.github/workflows/docs.yml` builds it
  for every pull request and deploys it from `main` to GitHub Pages, at
  <https://blindspotsoftware.github.io/dutctl/>, with the Hugo version it pins.
- `site/data/pages.yaml` places each file: its section, type and roles. A content adapter reads the
  files and makes the pages. A page's title is the file's first heading, its description the first
  paragraph, and a file gets no front matter. A page may show only some `##` sections of a file, so
  that a file that others link stays whole.
- Program output and code are embedded where the file says `<!-- embed: path -->` or `<!-- embed:
  path#Func -->`, which GitHub does not show. `task docs:generate` writes the output of `dutctl -h`,
  `dutagent -h` and `go doc ./pkg/module` to `site/generated/` before every build; it is never
  committed.
- A relative link resolves as on GitHub: to the page that shows the file, or else to the file on
  GitHub. A link to a Markdown file that does not exist, a file in `pages.yaml` that does not exist,
  or an embed that fails, fails the build.

### Consequences

- Good, because the files read the same on GitHub as before, and the site adds navigation, search
  and the structure of ADR-0041.
- Good, because generated pages always show the programs of the commit they are built from.
- Bad, because the site's templates are ours to maintain, about 350 lines.
- Bad, because Hugo deprecates settings quickly; the pinned version is updated on purpose, not by
  each build.
- Bad, because generated pages exist on the site only, and a page made by the content adapter gets
  no edit button.

### Confirmation

The docs workflow builds the site for every pull request, and fails on a missing file, a broken
Markdown link or a failed embed.

## More information

- [ADR-0041](0041-structure-the-documentation-by-diataxis-with-a-guide-per-role.md): the structure
  the site shows.
- [site/data/pages.yaml](../../site/data/pages.yaml): where each file appears.
