#!/usr/bin/env python3
# Copyright 2025 Blindspot Software
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

"""TCP proxy that corrupts one file transfer, for the dutctl checksum test.

It forwards client-to-agent traffic untouched until it sees a marker, then
flips one byte inside it, once, and forwards the result. The payload keeps its
length and the kernel recomputes the TCP checksum over the damaged bytes, so
the stream stays well-formed and only the file content is wrong. That is the
property that made the real fault (a router offload engine re-splitting packets
for a userspace VPN) invisible to every layer below the digest.

Targeting the marker rather than a byte offset keeps the corruption inside the
file payload, so it cannot land in an HTTP/2 frame header and desynchronise the
stream instead of damaging the file.

Usage:
    corrupt-proxy.py --listen 0.0.0.0:2025 --target 127.0.0.1:2024 \
                     --marker uploaded-by-file-module
"""

import argparse
import socket
import sys
import threading


def endpoint(value):
    """Parse host:port into a (host, port) tuple."""
    host, _, port = value.rpartition(":")
    if not host or not port:
        raise argparse.ArgumentTypeError(f"expected host:port, got {value!r}")

    return host, int(port)


class Corrupter:
    """Flips one byte inside the first marker occurrence it sees."""

    def __init__(self, marker):
        self.marker = marker
        self.done = threading.Event()

    def apply(self, data):
        if self.done.is_set():
            return data

        at = data.find(self.marker)
        if at < 0:
            return data

        self.done.set()
        flip = at + len(self.marker) // 2
        print(f"corrupted byte at offset {flip} of a {len(data)}-byte chunk", flush=True)

        return data[:flip] + bytes([data[flip] ^ 0x01]) + data[flip + 1:]


def pump(src, dst, corrupter):
    """Forward src to dst until EOF, corrupting when a corrupter is given."""
    while True:
        try:
            data = src.recv(65536)
        except OSError:
            break

        if not data:
            break

        if corrupter is not None:
            data = corrupter.apply(data)

        try:
            dst.sendall(data)
        except OSError:
            break

    try:
        dst.shutdown(socket.SHUT_WR)
    except OSError:
        pass


def handle(client, target, corrupter):
    upstream = socket.create_connection(target)
    # Only the client-to-agent direction is corrupted: that is the path a
    # firmware image takes on its way to the emulator.
    threading.Thread(target=pump, args=(client, upstream, corrupter), daemon=True).start()
    pump(upstream, client, None)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--listen", type=endpoint, required=True, help="host:port to accept on")
    parser.add_argument("--target", type=endpoint, required=True, help="host:port of the dutagent")
    parser.add_argument("--marker", required=True, help="byte string to corrupt inside")
    args = parser.parse_args()

    corrupter = Corrupter(args.marker.encode())

    srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(args.listen)
    srv.listen(8)
    print(f"proxy {args.listen} -> {args.target}, marker {args.marker!r}", flush=True)

    while True:
        conn, _ = srv.accept()
        threading.Thread(target=handle, args=(conn, args.target, corrupter), daemon=True).start()


if __name__ == "__main__":
    sys.exit(main())
