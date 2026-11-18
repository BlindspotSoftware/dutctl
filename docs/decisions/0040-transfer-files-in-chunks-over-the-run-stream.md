---
status: accepted
date: 2026-07-24
---

# Transfer files in chunks over the Run stream

## Context and problem statement

A module moves a file with `RequestFile` or `SendFile`, and the Run stream
carried it whole, in one `File` message
([ADR-0003](0003-run-a-command-over-one-bidirectional-stream.md)). Client and
agent held the whole file in memory, a flash image included, and while that
message was in flight nothing else on the stream moved: module output and
console input waited. A module could not bound a transfer either, as neither
call took a context.

How should files move over the Run stream?

## Considered options

- **One message per file**: keep `File` and `FileRequest`.
- **Chunks over the Run stream**: split each file into numbered chunks that
  share the stream with the other messages.

## Decision outcome

Chosen option: **chunks over the Run stream**, because memory then stays
bounded at one chunk on each side, and output and input keep flowing during a
transfer.

- `FileTransferRequest`, `FileChunk` and `FileTransferResponse` replace `File`
  and `FileRequest` in `RunRequest` and `RunResponse`. The old field numbers
  stay reserved, so a peer that speaks the old protocol fails to parse a
  transfer instead of misreading it.
- The agent gives each transfer an ID that is unique within the run. A chunk
  carries at most 1 MiB. Chunks are numbered from 0, and the last one is
  marked final.
- An upload sends its next chunk only after the agent has acknowledged the
  last one, and the agent acknowledges a chunk once the module has read it.
  The client cannot outrun the module.
- A download streams without acknowledgments per chunk: the flow control of
  the stream bounds how far the agent runs ahead. The client confirms the
  complete file.
- `RequestFile` and `SendFile` take a context that bounds the transfer; the
  session sets no limit of its own. `SendFile` takes the file size, reads the
  module's reader on the module's goroutine and returns once the client has
  confirmed the file, or with the reason it did not arrive. The module keeps
  its reader and closes it. A module can run a call that waits on a goroutine
  of its own, but it cannot wait for a call that returned early.
- A session carries one transfer at a time: `RequestFile` and `SendFile`
  return an error while another transfer of the session runs. No module moves
  two files at once, and runs of other commands or devices transfer side by
  side, each in its own session. The protocol keeps its transfer IDs, so a
  later release can allow several transfers in a session without breaking a
  module; taking them back after a release would.
- No download outlives its `SendFile`, so a run ends once its modules have
  returned ([ADR-0031](0031-keep-a-device-busy-until-its-modules-return.md)).
  An upload ends with the context the module passed to `RequestFile`. The
  context of `Run` ends when `Run` returns, so an upload on it that the module
  did not read to the end is dropped then.
- The client writes a download to a temporary file next to the destination and
  moves it into place when it is complete, so a broken download leaves the
  destination as it was. A failed download fails the run on the client.
- The client still reads and writes the paths the agent names
  ([ADR-0014](0014-let-the-agent-access-client-files-the-user-did-not-name.md)).

This supersedes ADR-0003, which moved a file whole in one message. Its other
points stay in force:

- Discovery and locking use unary RPCs: `List`, `Commands`, `Details`, `Lock`
  and `Unlock`.
- `Run` is a bidirectional stream that the client opens for one command. Its
  first `RunRequest` carries a `Command`: the device, the command and its
  arguments. The agent rejects a stream that starts with anything else.
- After that, the stream carries the command's `Print` output, its consoles
  ([ADR-0035](0035-frame-consoles-in-line-or-raw-mode.md)) and its file
  transfers. The module on the agent starts every transfer, and the agent
  refuses one it did not start.
- The agent ends the stream by returning from its handler; the handler's error,
  if any, is the status of the RPC. There is no end-of-run message.

### Consequences

- Good, because memory stays bounded on both sides, whatever the size of the
  file.
- Good, because module output and console input keep flowing during a
  transfer.
- Good, because a module bounds a transfer the way it bounds a subprocess.
- Good, because a module learns whether its download arrived, and closes its
  reader itself, as it does every other file.
- Bad, because agents and clients from before and after the change cannot
  transfer files with each other, so both are updated together. Breaking
  changes ship in the alphas
  ([ADR-0013](0013-release-v1-0-0-as-alpha-pre-releases.md)).
- Bad, because an upload costs one round trip per chunk.
- Bad, because a module that does something else while a file moves starts a
  goroutine for the transfer.
- Bad, because a module moves one file at a time.

### Confirmation

`TestRunEndsOnceTheClientHasTheDownload`,
`TestRunDropsUploadWhenModuleReturns` and
`TestRunEndsAnUnreadUploadWithItsModule` in
[cmds/dutagent/run_test.go](../../cmds/dutagent/run_test.go) cover how a run
ends with its transfers. `TestSessionCarriesOneTransferAtATime` and the other
tests in
[internal/dutagent/session/filetransfer_test.go](../../internal/dutagent/session/filetransfer_test.go)
and [cmds/dutctl/file_transfer_test.go](../../cmds/dutctl/file_transfer_test.go)
cover both ends of the protocol.

## More information

- Supersedes [ADR-0003](0003-run-a-command-over-one-bidirectional-stream.md).
- [ADR-0033](0033-add-the-module-api-to-the-primary-public-interfaces.md): the
  module API, which this changes, is a primary public interface.
- [docs/README.md](../README.md): the messages of a download and an upload.
- [protobuf/dutctl/v1/dutctl.proto](../../protobuf/dutctl/v1/dutctl.proto): the
  transfer messages.
