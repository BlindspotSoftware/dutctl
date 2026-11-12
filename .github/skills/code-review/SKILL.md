---
name: code-review
description: Review a dutctl pull request against the project's decision records and test layers. Use it to find which ADRs constrain the changed code, whether the change contradicts one or needs a new one, and which test layer the change needs.
---

# Reviewing a dutctl pull request

The rules in `.github/copilot-instructions.md` and `.github/instructions/` are short. This skill
holds the reference they point to:

- [decisions.md](decisions.md): every decision record (ADR) with the paths it governs and the
  constraint it puts on code, in one or two sentences.
- [test-layers.md](test-layers.md): which kind of change needs a test in which layer.

## Steps

1. Sort the changed files by area: a module (`pkg/module/<name>`), the module API
   (`pkg/module/module.go`), the agent's core (`cmds/dutagent`, `internal/dutagent`,
   `internal/chanio`), the client (`cmds/dutctl`, `internal/output`), the configuration grammar
   (`pkg/dut`, `docs/dutagent-config.md`), the protocol (`protobuf/`), CI (`.github/`,
   `.firmwareci/`), the decision records (`docs/decisions/`).
2. In `decisions.md`, find the ADRs that govern those paths. Check the diff against each one's
   constraint. If the diff contradicts an accepted ADR and does not add an ADR that supersedes it,
   say so, name the ADR and give its link (GEN-2).
3. Decide whether the change needs a new ADR (GEN-1): does it change a primary public interface, a
   rule every module must follow, how runs, devices, locks, cancellation or shutdown behave, a
   convention for the whole code base, or how the repository builds, tests or releases dutctl, or
   does it add a dependency or tool that would be hard to replace?
4. In `test-layers.md`, find the row for the change and check that the pull request brings the
   tests that row asks for (GEN-6).
5. Report each finding with the ID of the rule or ADR and its link. Comment only on what the pull
   request changes.

Superseded ADRs are listed only so you recognize them: never cite them as current.
