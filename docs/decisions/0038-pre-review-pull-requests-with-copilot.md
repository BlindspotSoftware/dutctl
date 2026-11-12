---
status: accepted
date: 2026-11-13
---

# Pre-review pull requests with Copilot

## Context and problem statement

dutctl's conventions are written down in CONTRIBUTING.md, in `docs/`, in READMEs, in doc and code
comments and in the decision records, and many of them are not checked by any tool: what a
module's `Init` may do, how the session's goroutines synchronize, which log level fits, whether a
change needs an ADR or contradicts one, which test layer a change needs. Reviewers point out the
same written rules again and again. GitHub Copilot's code review can check a pull request against
rules that the repository keeps, and links them in its comments. How do we use it without a second
set of conventions that drifts from the first?

## Decision drivers

- The written documents must stay the one source of each convention.
- The maintainers' review stays the decision; an automated review must not become a gate that
  contributors have to argue with.
- Copilot reads its instructions from the pull request's own branch, so a pull request can change
  the rules of its own review.

## Considered options

- **Copilot with repository rules by scope**: repository-wide rules, path-specific rules and a
  skill for long reference material, each rule summarizing a written convention.
- **One AGENTS.md for all agents**: one file that coding agents and Copilot's review both read.
- **No automated review**: the maintainers keep pointing out the rules by hand.

## Decision outcome

Chosen option: **Copilot with repository rules by scope**, because it brings each rule to the
pull requests it concerns and keeps the written documents the source.

An AGENTS.md would address coding agents, another audience, and would hold rules for writing code
and for reviewing it in one file that every agent reads. Without an automated review, the
maintainers keep spending their review on rules that are already written down.

- Copilot reviews every pull request on its own. Its comments are hints: it is no required check,
  and the merge criterion stays `All-Checks` with the maintainers' review
  ([ADR-0011](0011-build-and-test-every-commit-of-a-pull-request.md)).
- Rules that concern every pull request live in `.github/copilot-instructions.md`; rules for
  modules, the agent's core, tests and decision records in `.github/instructions/`, each applied
  to its paths; the constraints of all decision records and the table of test layers in the skill
  `.github/skills/code-review/`.
- Each rule summarizes a convention that a document states, links that document, and carries an
  ID that is never used again for another rule. A pull request that changes a convention changes
  its rule too: in the same commit as the document, or, when the document is a decision record,
  which goes into a `docs` commit of its own, in another commit. A rule leaves out what CI and the
  linters check, and what concerns single commits, which Copilot does not review.
- CODEOWNERS gives the team the files that steer Copilot's review: `.github/`, and the other
  instruction files and skill folders that Copilot also reads, wherever they are.

### Consequences

- Good, because a pull request learns about a written rule before the maintainers review it, with
  a link to the rule's source.
- Good, because the rules are plain text in the repository: they are reviewed like code, and they
  stay usable with another tool.
- Bad, because each rule has to be kept in step with its document, and no tool checks that.
- Bad, because Copilot does not apply every rule in every review and sometimes comments wrongly;
  contributors may have to answer comments that do not apply.
- Neutral, because the automatic review is a repository setting, and requiring the team's review
  for its files is a setting of the branch protection; neither is in the repository.

### Confirmation

Reviewers check that a pull request that changes a convention changes its rule too, and that a new
rule links a document that states it. Nothing checks this mechanically.

## More information

- [CONTRIBUTING.md](../../CONTRIBUTING.md#automated-review) describes the review for contributors.
- [ADR-0011](0011-build-and-test-every-commit-of-a-pull-request.md): the checks that gate a merge.
