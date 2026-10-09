# Manual testing on fwci-dutctl-tester-2

`fwci-dutctl-tester-2` is a Raspberry Pi kept for hands-on testing with the
dutctl CLI. Deploy a dutagent build from your checkout to it and poke at it
with your own `dutctl`; the build stays until the next deploy. It has no DUT hardware: no
power switch, flash programmer or PDU. Its serial port is a fake one that
echoes back whatever you send.

## Usage

```sh
task test:manual                  # deploy agent, build client, check every module
task test:manual -- serial file   # only these modules
test/manual/deploy.sh             # only deploy agent and config
```

`run.sh` prints each `dutctl` command before running it, so a failing check
can be rerun by hand. Its defaults, overridable through the environment, are
at the top of the script.

Deploying replaces the agent for everyone using the tester.

## Fake serial

`fake-serial.service` runs socat to join two ptys like a null-modem cable:

- `/run/fake-serial/ttyS2` is the port the `serial` command opens.
- `/run/fake-serial/ttyS1` is the DUT side. Write to it to play the DUT:
  `printf 'login: ' >/run/fake-serial/ttyS1`

`fake-serial-echo.service` sends everything that arrives on ttyS1 straight
back, so `dutctl … serial -- send hello expect hello` works without anyone on
the DUT side.

## How the tester is set up

`deploy.sh` cross-compiles dutagent for arm64, copies it with
`tester-2/config.yaml` to `~oscar/dutagent-deploy/`, and runs
`sudo -n /usr/local/sbin/dutagent-deploy`. That script validates the config
with the new build, installs both to `/usr/local/bin/dutagent` and
`/etc/dutagent/config.yaml`, and restarts the service. The packaged
`/usr/bin/dutagent` stays untouched; the `dutagent-manual.conf` drop-in points
the unit at the deployed build.

One-time setup, as `oscar` on the tester with the files from `tester-2/` in the
home directory and a dutctl `.deb` installed:

```sh
sudo apt-get install -y socat
sudo install -m 0644 fake-serial.service fake-serial-echo.service /etc/systemd/system/
sudo install -D -m 0644 dutagent-manual.conf /etc/systemd/system/dutagent.service.d/manual.conf
sudo install -m 0755 dutagent-deploy /usr/local/sbin/dutagent-deploy
sudo systemctl daemon-reload
sudo systemctl enable --now fake-serial fake-serial-echo
```
