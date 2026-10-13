# Packaging

This directory contains files that are included in Linux distribution packages, such as systemd service. These files are designed to make an application run correctly and securely.

The various distribution packages are created with [GoReleaser](https://goreleaser.com/), which internally uses [nFPM](https://nfpm.goreleaser.com/) packager. For more information see `.goreleaser.yaml` configuration file.

NOTE: We do not use `GoReleaser` to create releases, only to create Linux distribution packages.


## `dutagent.service`
A systemd service to run `dutagent`. It is hardened and locked down, so that it runs with least privilege possible, under non-root user. Defines arguments for the `dutagent`, networking port to use, location of configuration file, restart conditions, and so on.

### Stopping and restarting
With this unit, `systemctl stop` and `systemctl restart` do not cut running work short: `dutagent` takes no new work and waits until the running commands and device reservations have ended, then stops. Meanwhile the owner of a reservation can still run commands on that device, so a job can finish. An idle agent stops at once. To update a worker without waiting for the restart to finish, use `systemctl restart --no-block dutagent`. A reboot stops the agent the same way, but systemd forces the reboot after 30 minutes; stop the agent first.

To abort the running commands instead, signal `dutagent` once more; signalling it again while it aborts makes it exit at once:

```
systemctl kill --kill-whom=main dutagent
```

Leave out `--kill-whom=main` (`--kill-who=main` before systemd 252) and the signal also reaches the tools `dutagent` runs, such as a flash programmer, which then stop mid-operation. The unit sets `KillMode=mixed` and `TimeoutStopSec=infinity` for this; dutagent itself does not depend on systemd. The configuration is read only at start, so a changed configuration takes effect with the restart. [docs/dutagent-shutdown.md](../docs/dutagent-shutdown.md) shows the stages in detail.


## `packaging/dutagent.sysusers`
The systemd service needs a non-root user to run, with correct privileges (for example to access serial devices). For this we use `systemd-sysusers` tool to create a user and group with correct privileges. This is done automatically by `systemd` on installation of the distribution package.


## `packaging/dutagent.tmpfiles`
Just like with `sysusers` case, we use `systemd-tmpfiles` tool to create directories needed by `dutagent`, with correct ownership and permissions. This is done automatically by `systemd` on installation of the distribution package.


## `packaging/dutagent-usb-ports.rules`
A udev rule that lets the `plugdev` group, which `dutagent` belongs to, write the sysfs `disable` file of each USB hub port. `uhubctl` then switches port power through sysfs, and the kernel does not power a port again early. The `recover` option of the flash module relies on this to cut USB power for the full time; through libusb alone the port comes back after about 3 s, which is too short to reset a hung DediProg. Together with `ReadWritePaths=-/sys/devices` in `dutagent.service`, which lifts the read-only `/sys` of `ProtectKernelTunables` for these files.
