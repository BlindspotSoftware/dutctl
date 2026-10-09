#!/usr/bin/env bash
# Build dutagent from this checkout and install it, with tester-2/config.yaml,
# on the manual tester.
set -euo pipefail

TARGET=${TARGET:-oscar@fwci-dutctl-tester-2.firmwareci}

here=$(cd "$(dirname "$0")" && pwd)
root=$(git -C "$here" rev-parse --show-toplevel)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -C "$root" -o "$work/dutagent" ./cmds/dutagent
cp "$here/tester-2/config.yaml" "$work/config.yaml"

ssh "$TARGET" mkdir -p dutagent-deploy
scp -q "$work/dutagent" "$work/config.yaml" "$TARGET:dutagent-deploy/"
ssh "$TARGET" sudo -n /usr/local/sbin/dutagent-deploy
