#!/bin/sh
# Writes the output of the programs that the documentation site shows to
# site/generated, built from the source of this checkout (ADR-0042). Task
# runs it before every build of the site, and so does the docs workflow.
set -eu
cd "$(dirname "$0")/.."
out=site/generated
mkdir -p "$out/bin"
go build -o "$out/bin/" ./cmds/dutctl ./cmds/dutagent
"$out/bin/dutctl" -h 2> "$out/dutctl-help.txt"
"$out/bin/dutagent" -h 2> "$out/dutagent-help.txt"
go doc -all ./pkg/module > "$out/module-api.txt"
