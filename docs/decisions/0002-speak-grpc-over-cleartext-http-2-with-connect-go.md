---
status: accepted
date: 2024-08-13
---

# Speak gRPC over cleartext HTTP/2 with connect-go

## Context and problem statement

Client and agent talk over the network. Most calls are short: list the devices,
list the commands of a device. Running a command needs a long-lived stream in
both directions (see [ADR-0003](0003-run-a-command-over-one-bidirectional-stream.md)). The
plan for the first milestone named gRPC as the protocol, and the service was
defined in protobuf.

That left two questions: which Go implementation of gRPC to build on, and how
to keep the contract and the code generated from it.

## Decision drivers

- Integration with Go's standard `net/http` package.
- A library that is well documented and stable.

## Considered options

- **connect-go** (`connectrpc.com/connect`): generated handlers and clients
  that run on `net/http`.
- **grpc-go** (`google.golang.org/grpc`): Google's implementation, which brings
  its own HTTP/2 stack instead of the standard `net/http` package.

## Decision outcome

Chosen option: **connect-go**, because it uses the standard `net/http` package,
which integrates better than a separate HTTP stack, and the project is well
documented and stable.

- Clients speak the gRPC protocol (`connect.WithGRPC()`) over HTTP/2 without
  TLS (h2c). Servers such as dutagent serve h2c from a plain `net/http` server.
- The contract is one file,
  [protobuf/dutctl/v1/dutctl.proto](../../protobuf/dutctl/v1/dutctl.proto), in
  the package `dutctl.v1`.
- buf lints the proto with its DEFAULT rules and generates the Go code with
  `protoc-gen-go` and `protoc-gen-connect-go`. The generated code is committed
  under `protobuf/gen`.

### Consequences

- Good, because the servers are ordinary `net/http` servers: header timeouts,
  graceful shutdown and h2c come from the standard library.
- Good, because the wire protocol is gRPC, so other programs can call an agent
  with any gRPC client generated from the proto.
- Good, because `go install` builds the binaries without any protobuf tools.
- Bad, because h2c carries no TLS: calls are neither encrypted nor
  authenticated (see [ADR-0026](0026-defer-authentication-to-mtls.md)).
- Bad, because changes in `net/http` reach the agent directly. With Go 1.26 and
  1.25.13, the server's header timeout outlived the h2c handoff and cut every
  stream after 10 seconds; the servers now clear that deadline once a
  connection carries a request.

### Confirmation

[protobuf/README.md](../../protobuf/README.md) lists the steps after a change to
the proto: `buf lint`, then `buf generate`. No CI job runs buf yet.

## More information

- [internal/rpc](../../internal/rpc): the client and server setup.
- [ADR-0001](0001-split-dut-control-into-an-agent-and-a-thin-client.md): the client-agent split that this
  protocol serves.
- [ADR-0023](0023-reject-rpcs-from-another-major-version.md): the version header on every
  call.
- [ADR-0034](0034-version-the-relay-protocol-as-alpha.md): the relay service
  in a package of its own, `dutctl.relay.v1alpha1`.
- Later changes:
  - 2026-06: servers and clients set up h2c through the `Protocols` setting of
    `net/http` instead of `golang.org/x/net/http2`, whose h2c package is
    deprecated.
  - 2026-07: the transport setup of all three binaries moved into
    `internal/rpc`.
  - 2026-10: the default port changed from 1024 to 2024, and an address without
    a port got the default port.

Recorded retroactively in October 2026.
