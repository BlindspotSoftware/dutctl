#!/usr/bin/env bash
# Exercise a running dutagent module by module through the dutctl CLI.
# Usage: run.sh [module...]   modules: agent dummy shell time file serial
set -uo pipefail

DUTCTL=${DUTCTL:-dutctl}
AGENT=${AGENT:-fwci-dutctl-tester-2.firmwareci:2024}
DEVICE=${DEVICE:-fwci-dutctl-tester-2}
FAKE_SERIAL_INPUT=${FAKE_SERIAL_INPUT:-/run/fake-serial/ttyS1}
TARGET=${TARGET:-oscar@fwci-dutctl-tester-2.firmwareci}

MODULES=(agent dummy shell time file serial)

if [[ $DUTCTL == */* ]]; then
	DUTCTL=$(realpath "$DUTCTL")
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work" || exit 1

passed=0
failed=()

dutctl() {
	"$DUTCTL" -s "$AGENT" "$DEVICE" "$@"
}

# verdict NAME REGEX OUTPUT EXIT_CODE: an empty REGEX only requires exit code 0.
verdict() {
	local name=$1 want=$2 out=$3 rc=$4

	[[ -n $out ]] && sed 's/^/    /' <<<"$out"

	if [[ $rc -eq 0 ]] && { [[ -z $want ]] || grep -qPz -- "$want" <<<"$out"; }; then
		echo "  ✓ $name"
		passed=$((passed + 1))
	else
		echo "  ✗ $name (exit $rc, want /$want/)"
		failed+=("$name")
	fi
}

# check NAME REGEX ARGS...: run dutctl with ARGS and match its output.
check() {
	local name=$1 want=$2
	shift 2

	printf '\n▶ %s\n  $ %s\n' "$name" "$(printf '%q ' "$DUTCTL" -s "$AGENT" "$DEVICE" "$@")"

	local out rc
	out=$(dutctl "$@" 2>&1)
	rc=$?
	verdict "$name" "$want" "$out" "$rc"
}

test_agent() {
	check "system-info" "Linux $DEVICE" system-info
}

test_dummy() {
	check "dummy-status" 'Hello from dummy status module\nCalled with 0 arguments' dummy-status
	check "dummy-status with argument" 'Called with 1 arguments\nArg 0: hello' dummy-status hello
	check "dummy-status with configured arguments" 'Called with 2 arguments' dummy-status-args

	printf '\n▶ dummy-repeat\n'
	local out rc
	out=$(printf 'hello\nhello world\n' | dutctl dummy-repeat 2>&1)
	rc=$?
	verdict "dummy-repeat" 'Hello from dummy repeat module!\nEnter one word per line.' "$out" "$rc"

	echo hello >ft-source.txt
	check "dummy-file-transfer" 'Hello from dummy file transfer module' dummy-file-transfer ft-source.txt ft-dest.txt
	verdict "dummy-file-transfer result" 'processed by dummy.FT module' "$(cat ft-dest.txt 2>&1)" 0
}

test_shell() {
	check "run echo" 'hello-from-shell-module' run 'echo hello-from-shell-module'
	check "run hostname" "^$DEVICE" run hostname
	check "run pipe" 'one-two-three' run "echo one two three | tr ' ' '-'"
}

test_time() {
	check "wait configured" 'Waiting for 1s' wait

	local start=$SECONDS
	check "wait 3s" 'Waiting for 3s' wait 3s
	verdict "wait blocks" '' "elapsed=$((SECONDS - start))s" "$((SECONDS - start >= 3 ? 0 : 1))"
}

test_file() {
	echo uploaded-by-file-module >upload-source.txt
	check "upload" '' upload upload-source.txt
	check "upload arrived" 'uploaded-by-file-module' run 'cat /tmp/dutctl-manual-upload.txt'

	check "stage download" '' run 'echo downloaded-by-file-module >/tmp/dutctl-manual-download.txt'
	check "download" '' download download-target.txt
	verdict "download content" 'downloaded-by-file-module' "$(cat download-target.txt 2>&1)" 0

	check "clean up" '' run 'rm -f /tmp/dutctl-manual-upload.txt /tmp/dutctl-manual-download.txt'
}

test_serial() {
	check "serial send/expect via echo" '\[2/2\] matched' serial -t 5s -- send ping expect ping

	printf '\n▶ serial monitor\n'
	dutctl serial -t 4s >monitor.txt 2>&1 &
	local monitor=$!
	sleep 1
	# The agent runs one command per device at a time, so feed the DUT side over ssh.
	ssh "$TARGET" "printf 'Hello World!\r\n' >$FAKE_SERIAL_INPUT"
	wait "$monitor"
	local rc=$?
	verdict "serial monitor" 'Hello World!' "$(cat monitor.txt)" "$rc"
}

selected=("$@")
[[ ${#selected[@]} -eq 0 ]] && selected=("${MODULES[@]}")

for m in "${selected[@]}"; do
	declare -F "test_$m" >/dev/null || { echo "unknown module '$m'" >&2; exit 2; }
done

for m in "${selected[@]}"; do
	printf '\n=== %s ===\n' "$m"
	"test_$m"
done

printf '\n%d passed, %d failed\n' "$passed" "${#failed[@]}"
for f in "${failed[@]}"; do
	echo "  ✗ $f"
done
[[ ${#failed[@]} -eq 0 ]]
