# Use dutctl in scripts and CI

This guide shows how a script or a CI job uses a device: under a name of its own, with the device reserved for the
job, and with commands that must span others, such as a serial log across a power cycle.

## Give the job a name of its own

dutagent tells users apart by the name dutctl sends, `<user>@<host>` by default. All terminals and scripts on one
machine share that name, and may run commands on the same device at once. Give each job a name of its own with `-u`,
such as `-u "ci-${CI_JOB_ID:?}@runner"`, so jobs stay apart from each other and from you. Where `$USER` or the host
name is not set, as in some CI containers, always pass `-u`.

## Reserve the device and run the commands

Run commands side by side in a script only when one must span the others, such
as a serial log across a power cycle; otherwise configure one command. Call
dutctl directly, so that `$!` is dutctl's process, and let the background
command end by itself:

```sh
set -eu
U="ci-${CI_JOB_ID:?}@runner"; A=agent.lab:2024
dutctl -s "$A" -u "$U" dev lock 1h
trap 'dutctl -s "$A" -u "$U" dev unlock || true' EXIT
dutctl -s "$A" -u "$U" dev serial -t 3m -- expect 'login:' > serial.log 2>&1 &
mon=$!
for i in $(seq 100); do               # at most 20 s
  grep -q -- '--- Connected to' serial.log && break
  kill -0 "$mon" 2>/dev/null || { cat serial.log; exit 1; }
  sleep 0.2
done
grep -q -- '--- Connected to' serial.log
dutctl -s "$A" -u "$U" dev power off
sleep 2
dutctl -s "$A" -u "$U" dev power on
wait "$mon"                           # 0 once 'login:' was seen; 1 on timeout or any failure
```

Do not end a background dutctl with a signal (`kill`, Ctrl-C) before the next
step: dutctl returns at once while the agent still stops the command, and a next
step that runs the same command is refused.
