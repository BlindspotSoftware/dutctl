---
status: accepted
date: 2026-11-27
---

# Structure the documentation by Diátaxis with a guide per role

## Context and problem statement

The documentation grew one page per change: `docs/`, a README per module and the decision records.
Many pages mix a lesson, steps, facts and background, `docs/device-access.md` all four. Three kinds
of readers look for different things in them: users, who run commands on a deployed and configured
setup; admins, who wire devices to an agent and write its configuration; and module developers, who
write Go modules. Facts stand in several places and drift apart: the raw console keys in four, the
`Help` text of a module also in its README, the module interface also in the module guide. How
should the documentation be structured so that each reader finds an entry, each page serves one
need, and each fact is written once?

## Decision drivers

- A reader starts from what they want to do, and one person often has several roles.
- A page that serves one need is easier to follow and to keep correct than one that serves four.
- What the code can state, it states: a copy in a document drifts from it.
- Documentation ships with the change that makes it necessary (CONTRIBUTING.md), so where a change
  goes must be obvious.
- The files are linked from decision records, code and the review rules, and must keep their paths.

## Considered options

- **The four Diátaxis types at the top, the roles inside each**: tutorials, how-to guides,
  reference and explanation as the main sections.
- **One manual per role, the types inside each**: a user, an admin and a developer guide.
- **A guide per role for doing, shared sections for knowing**: tutorials and how-to guides by role,
  reference and explanation shared.
- **Pages by topic, as now.**

## Decision outcome

Chosen option: **a guide per role for doing, shared sections for knowing**, because it follows the
line along which the readers differ. What a reader does depends on their role: a user never wires a
GPIO pin, an admin needs no Go. What a reader looks up or wants to understand depends on the
product: the configuration file is the same file whoever reads it. The types at the top would send
a user through four sections full of admin tasks; a manual per role would repeat the shared
reference in each.

The documentation follows [Diátaxis](https://diataxis.fr):

- Each page has one type. A tutorial teaches by one safe path, a how-to guide leads a competent
  reader to a goal, reference describes the machinery, explanation discusses why. Content of
  another type is linked, not mixed in. The decision records are explanation.
- The sections are named by the goal: Get started (one tutorial in parts, from user to admin to
  module developer), Use dutctl (users), Set up agents (admins), Write modules (module developers),
  Reference and Concepts (shared, structured like the product), and Project (contributors). Every
  link in the menu and in the lists names the roles of its page by an icon.
- Each fact has one home, and others link to it. What the programs or the code can state is taken
  from them and embedded: the help of `dutctl -h` and `dutagent -h`, the doc comments of the module
  API, and code examples, which are the demo modules and the Go examples of `pkg/module`. Such text
  is improved where it is written, in the code.
- A list has at most seven entries, a section's page introduces what it lists, and no section
  exists before it has a page.
- A file keeps its path when it is split; the site places files by `site/data/pages.yaml`
  ([ADR-0042](0042-publish-the-documentation-with-hugo-and-relearn-on-github-pages.md)).

### Consequences

- Good, because each role has an entry, and each page serves one need.
- Good, because reference taken from the code cannot drift from it.
- Bad, because pages that mixed types become several files, and an author has to decide which type
  a text is.
- Bad, because the help texts of the programs and the doc comments become documentation that is
  reviewed as such, and grow.
- Neutral, because the project documents, such as CONTRIBUTING.md and the changelog, keep their
  form and are shown as project documents.

### Confirmation

The review rules for documentation in `.github/instructions/docs.instructions.md` and the skill
`.github/skills/docs-review/` check a pull request against these rules. The site's build fails when
a page, a linked Markdown file or an embedded file is missing.

## More information

- [CONTRIBUTING.md](../../CONTRIBUTING.md#documentation-style-guide) tells authors where a text goes.
- [ADR-0042](0042-publish-the-documentation-with-hugo-and-relearn-on-github-pages.md): how the site is
  built from the files.
- [ADR-0038](0038-pre-review-pull-requests-with-copilot.md): the review that applies the rules.
