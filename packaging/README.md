# Packaging

This directory contains files that are included in Linux distribution packages, such as systemd service. These files are designed to make an application run correctly and securely.

The various distribution packages are created with [GoReleaser](https://goreleaser.com/), which internally uses [nFPM](https://nfpm.goreleaser.com/) packager. For more information see `.goreleaser.yaml` configuration file.

NOTE: We do not use `GoReleaser` to create releases, only to create Linux distribution packages.


## `dutagent.service`
A systemd service to run `dutagent`. It is hardened and locked down, so that it runs with least privilege possible, under non-root user. Defines arguments for the `dutagent`, networking port to use, location of configuration file, restart conditions, and so on.

The unit sets `KillMode=mixed` and `TimeoutStopSec=infinity`, so that dutagent ends its commands in stages on a stop; dutagent itself does not depend on systemd. [Stop and restart dutagent](../docs/stop-dutagent.md) shows how to stop and restart it.


## `packaging/dutagent.sysusers`
The systemd service needs a non-root user to run, with correct privileges (for example to access serial devices). For this we use `systemd-sysusers` tool to create a user and group with correct privileges. This is done automatically by `systemd` on installation of the distribution package.


## `packaging/dutagent.tmpfiles`
Just like with `sysusers` case, we use `systemd-tmpfiles` tool to create directories needed by `dutagent`, with correct ownership and permissions. This is done automatically by `systemd` on installation of the distribution package.


## `packaging/dutagent.modules-load`
`dutagent.service` allows USB serial ports with `DeviceAllow=char-ttyUSB`, and systemd looks up the `ttyUSB` class only when the service starts. If no USB serial adapter was present at that time, for example because the adapter sits on a board that is still switched off after a reboot of the worker, the class does not exist yet, and every later open of a `/dev/ttyUSB*` fails with "operation not permitted" until `dutagent` restarts. This file is installed to `/usr/lib/modules-load.d/dutagent.conf`, so `systemd-modules-load` loads `usbserial`, which registers the `ttyUSB` class, at every boot before `dutagent` starts.
