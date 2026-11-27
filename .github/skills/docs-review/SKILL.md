---
name: docs-review
description: Review a dutctl pull request that changes documentation (docs/, a README, site/, the help texts of dutctl and dutagent, or doc comments of pkg/module) against Diátaxis and the structure of the documentation site. Use it to decide a text's type and place, to find text of another type in a page, and to find facts that a page copies instead of taking them from the code.
---

# Reviewing documentation

The rules DOC-1 to DOC-7 in `.github/instructions/docs.instructions.md` are short. This skill holds
the checks behind them. The documentation follows [Diátaxis](https://diataxis.fr) with a guide per
role (ADR-0041), and `site/data/pages.yaml` gives each file its section, type and roles (ADR-0042).

## Steps

1. For each changed page, find its entry in `site/data/pages.yaml`: its type and roles.
2. Decide the type of each changed text by two questions: does it inform action or cognition, and
   does it serve study or work? Action and study make a tutorial, action and work a how-to guide,
   cognition and work reference, cognition and study explanation. A text whose type is not its
   page's belongs on another page (DOC-1), in the section of its type and role (DOC-2).
3. Check the text against the checks of its type below (DOC-6).
4. Look for copied facts: help text, doc comments, code, a configuration, or the same fact on
   another page (DOC-4). For a change to a help text, a module's `Help()` or options, or a doc
   comment of `pkg/module`, look for prose and README sections that still describe the old
   behaviour (DOC-5).
5. For a page that is split or renamed, grep for links to it and to its headings (DOC-3).
6. Report each finding with the rule's ID and link, and name the place where the text belongs.

## Checks per type

### Tutorial: Get started

- One path that a learner can follow on their own machine; hardware it needs is named up front.
- Every step has a visible result: the text shows the output and says what to notice.
- No alternatives, no options, and no explanation beyond a sentence with a link.
- It says what we will do, not what you will learn, and ends at a working result.

### How-to guide: Use dutctl, Set up agents, Write modules, Project

- Titled by the task, as a competent reader would search for it; it starts from their goal, not
  from the tool.
- Steps and conditions ("If …, do …"); it may branch for real-world variants.
- It links reference for the options and explanation for the why; it neither teaches nor lists
  every option.

### Reference: Reference

- Structured like dutctl: dutctl, dutagent, configuration, modules, module API, protocol.
- It describes: neutral, complete, in a consistent form, such as tables of name, type, default and
  description.
- Short examples only; no steps, no rationale, no opinion.
- Taken from the code where the code states it.

### Explanation: Concepts and the decision records

- Titled "About …"; it answers why: context, alternatives, consequences.
- Bounded to one topic, and links the decision records behind it.
- No steps and no option tables.

## Roles and sections

- Users run commands on a deployed and configured setup; admins wire devices to an agent and write
  its configuration; module developers write Go modules; contributors maintain the project. A page
  aimed at two roles is often two pages, or shared reference.
- A section lists at most seven entries, and its page introduces what it lists. A section exists
  only once it has a page.
