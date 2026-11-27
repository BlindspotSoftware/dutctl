# Stop and restart dutagent

This guide shows how to stop or restart dutagent without cutting running work short, and how to abort the running
commands when you must. dutagent reads its configuration only at start, so a changed configuration takes effect with
a restart. [About the agent's lifecycle](./dutagent-shutdown.md) explains the stages behind it.

## Stop or restart the service

With the [packaged unit](../packaging/README.md), `systemctl stop` and `systemctl restart` do not cut running work
short: `dutagent` takes no new work and waits until the running commands and device reservations have ended, then
stops. Meanwhile the owner of a reservation can still run commands on that device, so a job can finish. An idle agent
stops at once.

```
systemctl restart dutagent
```

To update a worker without waiting for the restart to finish, use `systemctl restart --no-block dutagent`.

A reboot stops the agent the same way, but systemd forces the reboot after 30 minutes; stop the agent first.

## End a forgotten reservation instead

If the stop waits for a reservation that its owner forgot, end that reservation rather than every command:

```
dutctl <device> unlock force
```

## Abort the running commands

To abort the running commands instead, signal `dutagent` once more; signalling it again while it aborts makes it exit
at once:

```
systemctl kill --kill-whom=main dutagent
```

Leave out `--kill-whom=main` (`--kill-who=main` before systemd 252) and the signal also reaches the tools `dutagent`
runs, such as a flash programmer, which then stop mid-operation.

## Without systemd

In the terminal that runs dutagent, Ctrl-C does the same: the first stops taking new work, the second aborts the
running commands, a third exits at once. An agent started in the background of a script ignores `SIGINT`, the signal of
Ctrl-C, and one started with `nohup` ignores `SIGHUP`; send it `SIGTERM` with `kill`.
