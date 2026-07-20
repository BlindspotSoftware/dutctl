# Packaging

This directory contains files that are included in Linux distribution packages, such as systemd service. These files are designed to make an application run correctly and securely.

The various distribution packages are created with [GoReleaser](https://goreleaser.com/), which internally uses [nFPM](https://nfpm.goreleaser.com/) packager. For more information see `.goreleaser.yaml` configuration file.

NOTE: We do not use `GoReleaser` to create releases, only to create Linux distribution packages.


## `dutagent.service`
A systemd service to run `dutagent`. It is hardened and locked down, so that it runs with least privilege possible, under non-root user. Defines arguments for the `dutagent`, networking port to use, location of configuration file, restart conditions, and so on.


## `packaging/dutagent.sysusers`
The systemd service needs a non-root user to run, with correct privileges (for example to access serial devices). For this we use `systemd-sysusers` tool to create a user and group with correct privileges. This is done automatically by `systemd` on installation of the distribution package.


## `packaging/dutagent.tmpfiles`
Just like with `sysusers` case, we use `systemd-tmpfiles` tool to create directories needed by `dutagent`, with correct ownership and permissions. This is done automatically by `systemd` on installation of the distribution package.
