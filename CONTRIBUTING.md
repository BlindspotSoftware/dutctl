# Contributing to dutctl

👍🎉 First off, thanks for taking the time to contribute! 🎉👍

The following is a set of guidelines for contributing to the dutctl project, which are maintained by [Blindspot Software GmbH](https://blindspot.software/) on GitHub. These are mostly guidelines, not rules. Use your best judgment. 

If you discover a **security issue**, please bring it to our attention right away! Please refer to our [Security Policy](SECURITY.md) for information about reporting vulnerabilities.

Read our [Code of Conduct](CODE_OF_CONDUCT.md) to keep contributions approachable and respectable


Use the table of contents icon on the top right corner of this document to get to a specific section of this guide quickly.

The dutctl project follows a governance model that balances the leadership of Blindspot Software with community input. For details about how decisions are made and how you can participate, please read our [Governance document](GOVERNANCE.md).


## How Can I Contribute?

### Reporting Bugs

- **Ensure the bug was not already reported** by searching on GitHub under [Issues](https://github.com/BlindspotSoftware/dutctl/issues).
- If you're unable to find an open issue addressing the problem, [open a new one](https://github.com/BlindspotSoftware/dutctl/issues/new). Be sure to include a **title and clear description**, as much relevant information as possible, and a **code sample** or an **executable test case** demonstrating the expected behavior that is not occurring.
- Use the bug report template.

### Suggesting Features

- Open a feature request issue on the [issue tracker](https://github.com/BlindspotSoftware/dutctl/issues).
- Use the feature request template.

### Your First Code Contribution

Unsure where to begin contributing to dutctl? You can start by looking through these issues:

- [Beginner friendly issues](https://github.com/BlindspotSoftware/dutctl/labels/good%20first%20issue) - issues which should only require a few lines of code.
- [Help wanted issues](https://github.com/BlindspotSoftware/dutctl/labels/help%20wanted) - issues which should be a bit more involved than beginner-friendly issues.

### Pull Requests

1. **Fork and Pull Request**: All changes are made through pull requests.
2. **Issue First**: For significant changes, opening an issue for discussion before implementation is recommended.
3. **Test**: If you've added code that should be tested, add tests.
4. **Documentation**: New features should include appropriate documentation.
5. **Consider Draft PRs**: This way you can ensure the CI passes, before asking for review.
6. 🚀 Issue the pull request!

## Development Workflow

### Setting Up the Development Environment

1. **Install Go**: https://go.dev/learn/ , see [go.mod file](go.mod) for the minimal required version
2. **Clone the repository**: `git clone https://github.com/BlindspotSoftware/dutctl.git`
3. **Install dependencies**: `go mod download`
4. **Set up (optional) development tools**:
   - Install golangci-lint (see Code Quality section below)
   - Set up commit hooks for conventional commits (see Conventional Commits section below)

### Conventional Commits

This project uses [Conventional Commits](https://www.conventionalcommits.org/) for its commit message format.

Commit messages should follow this pattern:
```
<type>(<optional scope>): <description>

[optional body]

[optional footer(s)]
```

Where type is one of the following:
- **build**: Tooling, etc.
- **chore**: Housekeeping, dependency management, go.mod etc.
- **ci**: Continuous integration, workflows, etc.
- **docs**: Readme, doc comments
- **feat**: Source code changes introducing new functionality
- **fix**: Bug fixes, no new functionality
- **refactor**: Source code changes without changing behavior
- **revert**: Revert a commit
- **test**: Add tests, increase coverage, which were not committed initially with a fix or feat commit

#### Local linting minimizes overhead before creating a PR

If you want to enable commitlint locally, check out https://commitlint.js.org/guides/local-setup.html

> [!TIP]
> You can skip the husky part and just use GitHub hooks out of the box:

Rename `.git/hooks/commit-msg.sample` into `.git/hooks/commit-msg` and put in
```
#!/bin/sh
npx commitlint --edit
```
This change will not be propagated to the remote repo.

You can **bypass commitlint locally** like so: `git commit --no-verify -m"commitlint won't like"`


### Testing

- Write tests for new features and bug fixes
- Run tests locally before submitting PRs: `go test ./...`
- Aim for reasonable test coverage of new code

## Style Guides

### Go Code Style Guide

- Follow the standard Go [Code Review Comments](https://go.dev/wiki/CodeReviewComments) guidelines (The exception proves the rule ...)
- Follow the [Effective Go](https://golang.org/doc/effective_go) principles
- Document all exported symbols with proper Go doc comments

To automatically check for most of these styles and practices the CI runs [golangci-lint](https://golangci-lint.run/) to run a collection of linters. The rules and [settings](.golangci.yml) will be adapted as the project grows. Contributions are welcome here, too.

#### Run golangci-lint locally

For a faster development cycle, you can integrate [golangci-lint](https://golangci-lint.run/welcome/integrations/) into your local setup. The current version and configuration is pinned in `.golangci.yml`.

We recommend setting up your editor to run golangci-lint automatically. Most popular Go IDEs support this:

- **VS Code**: Use the Go extension which supports golangci-lint
- **GoLand**: Install the Golangci-lint plugin
- **Vim/Neovim**: Configure with ALE or similar linting engines

### Documentation Style Guide

- Use Markdown for documentation
- Keep README.md and other documentation up to date with code changes
- Use clear, concise language
- Include examples where appropriate

---

Thank you for contributing to dutctl! Your efforts help make this project better for everyone. ❤️