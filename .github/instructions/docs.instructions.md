---
applyTo: "docs/**,**/README.md,site/**,cmds/dutctl/dutctl.go,cmds/dutagent/dutagent.go,pkg/module/**"
---

# Review rules for documentation

The documentation follows Diátaxis with a guide per role, and the site takes its pages from the
files of the repository, placed by `site/data/pages.yaml`. The "Documentation Style Guide" in
CONTRIBUTING.md is the source of these rules; the skill `.github/skills/docs-review/` holds the
checks per type.

## Type and place

- [DOC-1] A page is of one type: tutorial, how-to guide, reference or explanation. Text of another
  type in it, such as steps in an explanation, instructions or rationale in reference, or options
  and explanation in a tutorial, belongs on a page of its own, linked.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/CONTRIBUTING.md#documentation-style-guide
- [DOC-2] A page's entry in `site/data/pages.yaml` puts it in the section of its type and role, with
  the roles it serves: tutorials in Get started, how-to guides in the guide of their role, reference
  in Reference, explanation in Concepts. A section lists at most seven entries.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/CONTRIBUTING.md#documentation-style-guide
- [DOC-3] A split keeps the file for one of its parts, and keeps the headings that others link, as
  the code and these rules do, so that no link breaks.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/CONTRIBUTING.md#documentation-style-guide

## One home per fact

- [DOC-4] A text does not copy what the code states: the help of `dutctl -h` and `dutagent -h`, the
  doc comments of `pkg/module`, code or a configuration file. It links it, or shows it with an
  `<!-- embed: path -->` line; an improvement goes into the help text or the doc comment.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/CONTRIBUTING.md#documentation-style-guide
- [DOC-5] A change to the help text of dutctl or dutagent, to a module's `Help()` or options, or to a
  doc comment of `pkg/module` updates the pages that describe the same in prose, and the module's
  README: its arguments follow `Help()`, its options the module's struct.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/CONTRIBUTING.md#documentation-style-guide

## Per type

- [DOC-6] A tutorial follows one path that works, without options, and shows what each step prints.
  A how-to guide is titled by its task and starts from the reader's goal. Reference describes and
  does not instruct. Explanation is titled "About …" and links the decision records behind it.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/.github/skills/docs-review/SKILL.md
- [DOC-7] A module's README starts with its title, names the IDs of its modules, and has the
  sections Arguments, Configuration Options and Example, per module in a package of several.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/CONTRIBUTING.md#documentation-style-guide
