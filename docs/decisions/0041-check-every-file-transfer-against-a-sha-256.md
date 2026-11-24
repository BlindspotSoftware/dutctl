---
status: accepted
date: 2026-11-03
---

# Check every file transfer against a SHA-256

## Context and problem statement

TCP and HTTP/2 check their framing, not the content they carry. A path that
damages bytes and computes the checksums below it again, such as a network
card that splits packets again for a VPN in user space, delivers a file of the
right length with the wrong content. Firmware images move over these
transfers, and a damaged image written to a flash chip can leave a device that
does not boot. [ADR-0040](0040-transfer-files-in-chunks-over-the-run-stream.md)
left room for a checksum on the chunks without a change to the module API.

## Considered options

- **Required digest**: a final chunk without a digest breaks the protocol.
- **Optional digest**: a receiver takes a file without a digest unchecked and
  logs a warning, as ADR-0040 asks of a field that a later change adds.

## Decision outcome

Chosen option: **Required digest**, because no release has the chunked
transfer without the digest. The digest is part of the first version of the
protocol, not a later change under the rules of ADR-0040, so there is no older
peer to stay compatible with, and an optional digest would only let a file
pass unchecked.

Every file transfer carries the SHA-256 of its file, and the receiver checks
it before the file counts.

- The sender hashes the file as it sends it and puts the raw 32-byte digest on
  the final chunk (`FileChunk.sha256`).
- The agent checks an upload before the final chunk reaches `RequestFile`, so
  `RequestFile` returns an error instead of nil. The client checks a download
  before it moves the file into place, so a damaged file never replaces the
  destination; it ends the transfer with `ERROR`, and the module's `SendFile`
  returns an error.
- A damaged upload fails the run with `DataLoss`. This adds a code to the
  mapping of
  [ADR-0025](0025-map-errors-to-connect-codes-only-in-rpc-handlers.md): the
  request was valid and no module failed, so neither `InvalidArgument` nor
  `Aborted` fits, and `Internal` would hide that the transport is at fault.
- A final chunk without a digest breaks the protocol. The agent ends the run
  with `InvalidArgument`, and the client fails the download.
- The client shows the digest of each file it moved, so a user can compare it
  with the digest of the source.

### Consequences

- Good, because a file damaged in transit fails the run instead of reaching a
  device.
- Good, because a script can tell a damaged upload (`DataLoss`) from a failed
  module (`Aborted`) and send the file again.
- Bad, because `RequestFile` writes the file to the module's writer as it
  arrives, so the writer holds damaged content until `RequestFile` returns the
  error. A module acts on the file only once `RequestFile` returned nil.
- Bad, because a build from between the chunked transfer and the digest
  cannot transfer files with a later one. No release has such a build.
- Bad, because both sides hash every byte, which costs CPU time on a small
  agent.

### Confirmation

`TestUploadDigest` in
[internal/dutagent/session/filetransfer_test.go](../../internal/dutagent/session/filetransfer_test.go),
the case "upload arrives damaged" of `TestRunHoldsDeviceUntilModulesReturn` in
[cmds/dutagent/run_test.go](../../cmds/dutagent/run_test.go),
`TestHandleFileChunk_UnverifiedDownloadKeepsOldFile` in
[cmds/dutctl/file_transfer_test.go](../../cmds/dutctl/file_transfer_test.go),
and `TestModulesFileChecksum` in
[test/integration/modules_test.go](../../test/integration/modules_test.go),
which damages an upload between client and agent.

## More information

- [ADR-0025](0025-map-errors-to-connect-codes-only-in-rpc-handlers.md): the
  mapping of errors to connect codes, which this adds `DataLoss` to.
- [ADR-0040](0040-transfer-files-in-chunks-over-the-run-stream.md): the
  chunked transfer that carries the digest.
- [internal/digest](../../internal/digest): the hash helpers both sides use.
