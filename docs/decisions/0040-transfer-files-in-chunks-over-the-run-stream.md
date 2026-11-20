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
bounded at one chunk on each side, and output and input pass between two
chunks of a transfer.

- `FileTransferRequest`, `FileChunk` and `FileTransferResponse` replace `File`
  and `FileRequest` in `RunRequest` and `RunResponse`. The old field numbers
  stay reserved, so a peer that speaks the old protocol fails to parse a
  transfer instead of misreading it.
- The agent gives each transfer an ID that is unique within the run. A chunk
  carries at most 1 MiB. Chunks are numbered from 0, and the last one is
  marked final.
- Both directions stream without acknowledgments per chunk. A transfer holds
  its direction of the stream for one chunk at a time, so other messages pass
  between two chunks, and a peer handles them there. The receiving side takes
  the next chunk only once it has written the last one, so the flow control of
  the stream bounds how far the sender runs ahead. The receiving side confirms
  the complete file.
- `RequestFile` and `SendFile` take a context that bounds the transfer; the
  session sets no limit of its own. Both work on the module's goroutine and
  return once the transfer ended. `RequestFile` writes the file to the
  module's writer and returns once the whole file is in it. `SendFile` takes
  the file size, reads the module's reader and returns once the client has
  confirmed the file. Each returns the reason when the file did not arrive.
  The module keeps its reader or writer and closes it. A module that does
  something else meanwhile runs the call on a goroutine of its own.
- The writer of `RequestFile` must not wait for the user's input: that input
  can wait behind the next chunk, which waits for the writer. The session
  promises only that other messages pass between two chunks, not while a
  writer is slow.
- A session carries one transfer at a time: `RequestFile` and `SendFile`
  return an error while another transfer of the session runs. No module moves
  two files at once, and runs of other commands or devices transfer side by
  side, each in its own session. The protocol keeps its transfer IDs, so a
  later release can allow several transfers in a session without breaking a
  module; taking them back after a release would.
- No transfer outlives its call, so a run ends once its modules have returned
  ([ADR-0031](0031-keep-a-device-busy-until-its-modules-return.md)).
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
- Good, because module output and console input pass between two chunks of a
  transfer.
- Good, because a module bounds a transfer the way it bounds a subprocess.
- Good, because a module learns whether its file arrived, and closes its
  reader or writer itself, as it does every other file.
- Good, because no transfer runs alongside the module that started it, so a
  module has no transfer to end before it returns.
- Bad, because agents and clients from before and after the change cannot
  transfer files with each other, so both are updated together. Breaking
  changes ship in the alphas
  ([ADR-0013](0013-release-v1-0-0-as-alpha-pre-releases.md)).
- Bad, because a module that does something else while a file moves starts a
  goroutine for the transfer.
- Bad, because the user's input can wait while the module's writer writes a
  chunk, so a writer that waits for that input blocks the run.
- Bad, because a module moves one file at a time.

### Confirmation

`TestRunEndsOnceTheClientHasTheDownload` in
[cmds/dutagent/run_test.go](../../cmds/dutagent/run_test.go) covers how a run
ends with its transfers. `TestSessionCarriesOneTransferAtATime`,
`TestUploadHappyPath`, `TestRequestFileWriteErrorFailsTheUpload` and the other
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
