---
applyTo: "docs/decisions/**"
---

# Review rules for decision records

`docs/decisions/` holds the project's decision records (ADRs) in the MADR format, one file per
decision, numbered in order. `README.md` and `template.md` in this folder are no records; the
rules below do not apply to them. CI already checks the file name, the unique number, the front
matter's presence, the status value, that the ADR a `superseded by` status names exists, and that
a commit changing an ADR is a `docs:` commit that changes nothing outside this folder. Review what
CI cannot check.

- [ADR-1] The front matter holds `status` and `date`. The date is the day the decision was taken,
  or proposed, and stays when the status changes, also on acceptance.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/README.md#status
- [ADR-2] The title (H1) is an imperative phrase that names the decision, without a number, and
  the file name is the next free number plus the title in kebab case. The record is short, lists
  only options that were actually weighed, and drops optional sections that would stay empty.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/README.md#how-to-add-one
- [ADR-3] An ADR links other ADRs, documentation and code, never pull requests or issues: it
  explains its decision on its own.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/README.md#how-to-add-one
- [ADR-4] An ADR that comes with the change implementing it is `accepted` in the same pull request.
  An ADR that needs agreement before any code is written is `proposed`, in a pull request of its
  own.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/README.md#how-to-add-one
- [ADR-5] An accepted ADR keeps its reasoning as it was. It gets only fixes of typos and links
  and, in a record written after the fact, a note under More information on a later change that
  needed no ADR of its own. To change a decision, a new ADR supersedes it; when the new ADR lands
  as accepted, the same pull request sets the old one's status to `superseded by ADR-NNNN` and
  links the new one under More information. A new ADR that only adds to an earlier one leaves it
  accepted and says what it adds, and the earlier one links it under More information. An older
  ADR never links a proposal.
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/README.md#changing-a-decision
- [ADR-6] Check a new or changed ADR against the accepted ones, listed with their constraints in
  the `code-review` skill. If it contradicts one without superseding it, ask for the supersede, or
  for the contradiction to go. A new ADR also gets its entry in the skill's `decisions.md`
  (GEN-15).
  → https://github.com/BlindspotSoftware/dutctl/blob/main/docs/decisions/README.md#changing-a-decision
