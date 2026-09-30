# Packaging

This directory contains files that are included in Linux distribution packages, such as systemd service. These files are designed to make an application run correctly and securely.

The various distribution packages are created with [GoReleaser](https://goreleaser.com/), which internally uses [nFPM](https://nfpm.goreleaser.com/) packager. For more information see `.goreleaser.yaml` configuration file.

NOTE: We do not use `GoReleaser` to create releases, only to create Linux distribution packages.


## `dutagent.service`
A systemd service to run `dutagent`. It is hardened and locked down, so that it runs with least privilege possible, under non-root user. Defines arguments for the `dutagent`, networking port to use, location of configuration file, restart conditions, and so on.

### Updating a worker without breaking jobs
Restart the service after a package or configuration update with:

```
systemctl restart --no-block dutagent
```

On SIGTERM, `dutagent` shuts down gracefully:

- It takes no new work: a new reservation (`dutctl lock`) and a command on a
  device nobody has reserved are refused with "dutagent is shutting down".
  FirmwareCI treats that like a busy device and tries again later.
- Running jobs finish. A job is a device reservation, which is how FirmwareCI
  holds a device for a whole job: its owner can keep running commands, extend
  and release it. Once no reservation is left (released or expired), the agent
  stops, and systemd starts it again. An idle agent stops at once.
- Commands running without a reservation get 15 seconds, as before. Reserve
  the device if your work must survive a restart.
- The wait is bounded by `-drain-timeout` (9h by default, enough for the
  longest FirmwareCI job). To stop at once, send the signal again:
  `systemctl kill --kill-whom=main dutagent`.

The service sends SIGTERM to `dutagent` only (`KillMode=mixed`), so a flash
tool it runs is not killed underneath it. The configuration file is read once at
start, so a changed configuration takes effect with the restart, never during a
running job.


## `packaging/dutagent.sysusers`
The systemd service needs a non-root user to run, with correct privileges (for example to access serial devices). For this we use `systemd-sysusers` tool to create a user and group with correct privileges. This is done automatically by `systemd` on installation of the distribution package.


## `packaging/dutagent.tmpfiles`
Just like with `sysusers` case, we use `systemd-tmpfiles` tool to create directories needed by `dutagent`, with correct ownership and permissions. This is done automatically by `systemd` on installation of the distribution package.
