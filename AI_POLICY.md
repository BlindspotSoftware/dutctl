# AI Usage Policy

> **Attribution:** This policy is adapted from the
> [u-root project's AI_POLICY.md](https://github.com/u-root/u-root/blob/main/AI_POLICY.md),
> which was adapted from the
> [Delve project's AI_POLICY.md](https://github.com/go-delve/delve/blob/master/Documentation/AI/AI_POLICY.md),
> which in turn was adapted from the
> [Ghostty project's AI_POLICY.md](https://github.com/ghostty-org/ghostty/blob/main/AI_POLICY.md).
> We are grateful to the u-root, Delve and Ghostty maintainers for providing
> a clear and thoughtful framework for AI usage in open source projects.

The dutctl project has strict rules for AI usage:

- **All AI usage in any form must be disclosed.** You must state
  the tool you used (e.g. Claude Code, Cursor, Amp) along with
  the extent that the work was AI-assisted. Put this disclosure in
  free-form into the description of your pull request, issue or
  discussion.

- **You are the author, not your AI tool.** AI tools or models must
  not be added as `Co-authored-by` trailers. Some tools add one by
  default, so remove it. The `Signed-off-by` line required by the
  [Developer Certificate of Origin](CONTRIBUTING.md#developer-certificate-of-origin)
  is a certification only a human can make: by signing off, you vouch
  for every line of the change, including the ones an AI wrote.

- **Pull requests created by AI must have been fully verified with
  human use.** AI must not create hypothetically correct code that
  hasn't been tested. Importantly, you must not allow AI to write
  code for platforms or environments you don't have access to manually
  test on. For dutctl, this applies above all to modules: a module
  drives real hardware, so a change to a module  must have been run by
  you against that hardware.

- **Issues and discussions can use AI assistance but must have a full
  human-in-the-loop.** This means that any content generated with AI
  must have been reviewed _and edited_ by a human before submission.
  AI is very good at being overly verbose and including noise that
  distracts from the main point. Humans must do their research and
  trim this down.

Contributions that do not follow these rules may be closed without
further review.

These rules apply only to outside contributions to dutctl. The
maintainers (see our [Governance document](GOVERNANCE.md)) are exempt
from these rules and may use AI tools at their discretion; they've
proven themselves trustworthy to apply good judgment.

## There are Humans Here

Please remember that dutctl is maintained by humans.

Every discussion, issue, and pull request is read and reviewed by
humans (and sometimes machines, too). It is a boundary point at which
people interact with each other and the work done. It is rude and
disrespectful to approach this boundary with low-effort, unqualified
work, since it puts the burden of validation on the maintainer.

In a perfect world, AI would produce high-quality, accurate work
every time. But today, that reality depends on the driver of the AI.
And today, most drivers of AI are just not good enough. So, until either
the people get better, the AI gets better, or both, we have to have
strict rules to protect maintainers.

## AI is Welcome Here

As a project, we welcome AI as a tool! The dutctl maintainers use AI
coding assistants in their own work, too.

**Our reason for the AI policy is not due to an anti-AI stance**, but
instead intended to help produce high quality code and maintain code
quality and overall standards when using AI coding assistance. This policy
also gives a common framework to use and foster effective open source
collaboration in the era of AI Agentic coding.

We, the dutctl maintainers at Blindspot Software, include this section
to be transparent about the project's use of AI for people who may
disagree with it, and to address the misconception that this policy is
anti-AI in nature.
