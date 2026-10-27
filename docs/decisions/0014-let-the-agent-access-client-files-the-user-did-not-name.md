---
status: accepted
date: 2025-12-19
---

# Let the agent access client files the user did not name

## Context and problem statement

During a run, the agent can ask the client for a file (`FileRequest`) or send
it one (`File`); either message names a path on the client. From September
2024 the client checked each such path against the command's arguments. The
path had to appear in one of them, possibly as part of a longer argument, or
the client refused the transfer and ended the run.

The check was meant to keep the agent from reading or writing files on the
user's machine that the user had not named. In July 2025 the maintainers asked
whether it was too strict, since a file could only move if the user typed its
name. A contributor replied that it was right for security: naming a file on
the command line is how the user agrees to hand it over.

In December 2025 the file module arrived. Its configuration can fix the source
or the destination of a transfer, so a command may move a file to or from a
fixed client path, such as an upload of `./configs/device.conf` that takes no
arguments. The client check rejected such transfers.

For the agent side, the maintainers had already decided that protection
against directory traversal belongs in the modules: the session hands file
contents to modules as readers and never opens a file itself.

## Considered options

- **Keep the client check**: a transferred path must appear in the command's
  arguments, so the user names every file that a run reads or writes.
- **Ask the user to accept each file**: the client asks before it reads or
  writes a path that the agent names.
- **Trust the agent and leave path checks to the modules**: the client reads
  and writes the paths the agent names; modules decide which paths to accept.

## Decision outcome

Chosen option: **trust the agent and leave path checks to the modules**,
because the check rejected transfers whose client path comes from the agent
configuration rather than from the command line, which the file module was
built to support. The check was removed with the file module, for the time
being. A further remedy, such as asking the user to accept each file, was
called for then but has not been built.

- The client reads the file that the agent requests and writes the file that
  the agent sends, at the path the agent names. It creates new files with mode
  0600 and overwrites existing ones.
- The agent configuration and the modules it uses decide which client files a
  command reads or writes. The user does not have to name them.
- A module that takes a path from the command line is responsible for
  checking it. The file module, for example, accepts destinations from the
  command line only as relative paths without a leading `..`, and uses paths
  fixed in the agent configuration as written.

### Consequences

- Good, because a command can move a file to or from a fixed place on the
  client without the user typing the path.
- Good, because the client needs no rule for which of the command's arguments
  name files.
- Bad, because the client trusts the agent: during a run, the agent and its
  configuration can read any file the user can read, and create or overwrite
  any file the user can write. The transport does not authenticate the agent
  either.
- Bad, because each module that transfers files must check the paths it takes
  from the command line itself; the core does not.
- Neutral, because the client reports each file it sent or received in its
  output, but only after the transfer.

### Confirmation

`TestParsePaths` in
[pkg/module/file/paths_test.go](../../pkg/module/file/paths_test.go) covers
which paths the file module accepts from the command line.

## More information

- [ADR-0003](0003-run-a-command-over-one-bidirectional-stream.md): the Run stream that carries
  file requests and files.
- [ADR-0026](0026-defer-authentication-to-mtls.md): why the transport
  authenticates neither side yet.
- [docs/README.md](../README.md): the upload and download messages on the Run
  stream.
- [pkg/module/file/README.md](../../pkg/module/file/README.md): the file
  module's path rules.
- [cmds/dutctl/rpc.go](../../cmds/dutctl/rpc.go): the client's handling of file
  requests and files.

Recorded retroactively in October 2026.
