# Contributing to dutctl

> [!NOTE]  
> Until MVP is finished, external contributions most likely won't be handled.

## Conventional Commits
The projects uses [Conventional Commits](https://www.conventionalcommits.org/) 

Commit messages are supposed to look like this: 
```
<type>(<optional scope>): <description>

[optional body]

[optional footer(s)]
```

Where type is one of the following:
- **build** tooling, taskfile, etc
- **chore** housekeeping, dependency management, go.mod etc.
- **ci** continuous integration, workflows, etc.
- **docs** Readme, doc comments
- **feat** Source code changes introducing new functionality
- **fix** Bug fixes, no new functionality
- **refactor** Source code changes without changing behavior
- **revert** Revert a commit
- **test** Add tests, increase coverage, which where not committed initially with a fix or feat commit


### Local linting minimizes overhead before MR

If you want to enable commitlint locally, check out https://commitlint.js.org/guides/local-setup.html

> [!TIP]
> You can skip the husky part and just use github hooks out of the box:

Rename `.git/hooks/commit-msg.sample` into `.git/hooks/commit-msg` and put in
```
#!/bin/sh
npx commitlint --edit
```
This change will not be populated to the remote repo.

You can **bypass commitlint locally** like so: `git commit --no-verify -m"commitlint won't like"`
