# Decision records

This folder records the decisions that shaped DUT Control and the way this
repository builds, tests and releases it: what was decided, why, which options
were weighed, and what we accept in exchange. Each decision is one Markdown
file in the [MADR](https://adr.github.io/madr/) format. Whatever its topic, we
call such a file an ADR, short for architecture decision record. Read the ADRs
of the part you work on before you change how it behaves, and write one when
you take a decision that others would otherwise have to rediscover from the
code or its history.

There is no index to keep up to date. The files are numbered in order, so the
folder listing is the log, oldest first; on GitHub it is the list above this
text. Each ADR keeps its status and date in its front matter, which GitHub shows
as a table at the top of the file. To see all statuses in a checkout:

```sh
grep -H '^status:' docs/decisions/[0-9]*.md
```

## When to write an ADR

Write an ADR for a change that:

- changes a primary public interface: the agent configuration grammar, the
  RPC protocol or the module API in `pkg/module` (see
  [Public Interfaces](../../README.md#public-interfaces));
- changes a rule that every module must follow;
- sets or changes how runs, devices, locks, cancellation or shutdown behave;
- adds a dependency or tool that would be hard to replace;
- sets a convention for the whole code base, such as for logging, errors or
  tests;
- changes how this repository builds, tests or releases DUT Control, such as
  the checks that CI runs or how versions are numbered;
- reverses an earlier ADR.

Features that follow the existing patterns, bug fixes and refactorings that
keep every contract need no ADR.

## How to add one

1. Discuss first where it helps: in an issue labelled `needs design` or in the
   Design Proposals discussions. The ADR records the outcome; it does not
   replace the discussion.
2. Copy [template.md](template.md) to `NNNN-title-in-kebab-case.md`, where
   `NNNN` is the next free four-digit number. CI checks the file name and that
   no other ADR has the number.
3. Fill it in and keep it short. The title is an imperative phrase that names
   the decision, such as "Stop the agent in stages". List only options that
   were actually weighed, and remove optional sections that would stay empty.
   Link other ADRs, documentation and code, but not pull requests or issues:
   an ADR explains its decision on its own.
4. Commit the ADR as a `docs:` commit of its own that changes nothing outside
   this folder, even when the same pull request implements the decision. CI
   checks this; the reasons are in
   [CONTRIBUTING.md](../../CONTRIBUTING.md#documentation-commits).
5. Open a pull request. An ADR that comes with the change that implements it
   goes into the same pull request with the status `accepted`. An ADR that
   needs agreement before any code is written goes into a pull request of its
   own with the status `proposed`; it becomes `accepted` when the maintainers
   merge it (see [GOVERNANCE.md](../../GOVERNANCE.md)).
6. If another pull request took your number in the meantime, take the next free
   one when you rebase, and update links to your file.

## Status

| Status                   | Meaning                                         |
| ------------------------ | ----------------------------------------------- |
| `proposed`               | Under discussion, or decided but not in force.  |
| `accepted`               | In force.                                       |
| `rejected`               | Weighed and declined; kept to show why.         |
| `deprecated`             | No longer in force, and nothing replaces it.    |
| `superseded by ADR-NNNN` | Replaced by a later ADR, linked from this one.  |

CI checks that every ADR has one of these statuses, and that a superseded ADR
names another ADR that exists.

The `date` is the day the decision was taken or, for a proposal, the day it was
proposed. It stays when the status changes, also when a proposal is accepted.

## Changing a decision

An accepted ADR keeps its reasoning as it was when the decision was taken. To
change a decision, write a new ADR that supersedes the old one. When the new ADR
lands as accepted, the same pull request sets the old ADR's status to
`superseded by ADR-NNNN` and adds a link to the new one under More information.
A new ADR that only adds to an earlier decision, without reversing any of it,
leaves the earlier ADR accepted: the earlier ADR links the new one under More
information, and the new one says what it adds. Older ADRs do not link to
proposals. Apart from that, an accepted ADR only gets fixes of typos and links.

## Records written after the fact

The ADRs that end with "Recorded retroactively in October 2026." were written
to record decisions taken before this folder existed. Their date is the day
the decision took effect, usually the merge of the change that implemented it.
They list considered options only where alternatives were weighed at the time,
and they name later changes to a decision under More information when those
did not need an ADR of their own.
