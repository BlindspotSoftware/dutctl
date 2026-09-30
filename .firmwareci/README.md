# FirmwareCI configuration for dutctl testing

This directory contains FirmwareCI configurations and tests.

For detailed information on FirmwareCI, please refer to the [official documentation](https://docs.firmware-ci.com/).


## Requirements

- Parametric and re-usable testing
- Easy to expand
- Reliable
- Can be used in CI/CD to verify each pull request


## Design decisions

### One test per module, shared setup on the DUT
Each dutctl module gets its own test file, so a failure names the module that
broke instead of one combined run.

The serial module is still one module, but its tests are split across three
test files (see [workflows](#workflows)). The reconnect test stops and restarts
the fake serial, so it runs as its own job. The terminal and reconnect scenarios
run the installed `dutctl` on the tester itself: raw mode needs a real
pseudo-terminal (`script`), and the reconnect steps must react to what the
client prints.

The setup and tear-down around every test is the same and rather involved:

- copy over the compiled binaries
- copy over configuration files
- spin up the server (agent)
- execute the test
- shut down the server
- delete the copied binaries
- delete configuration files

For the serial feature it also means spinning up a "fake" serial to test against.

Rather than repeating that in every test, it lives once in the DUT's own
`pre.yaml` and `post.yaml`. FirmwareCI applies them to every test that runs on
this DUT, and a test that needs something different declares its own
`pre-stage` / `post-stage` to override them.

### Debian packaging magic
As you might notice, instead of simply copying over the compiled binaries (`dutctl` / `dutagent` / ...) we use Debian packages.

The biggest motivator here to do this was to accommodate for setup and tear-down, and the differences between tested versions (future-proofing). This way, we leverage the power of package manager to make sure that the cleanup (uninstalling) of old file is complete and that no files are left behind! This makes sure that the environment is always pristine.

Another huge advantage is, that with this approach, that run-time dependencies are also handled by package manager. Because of this, the test hardware (Raspberry Pi) has minimal (if any) setup required for use in the FWCI tests. Just install package, test and uninstall! Easy!


## Project structure

```
.firmwareci/
├── Taskfile.yml
├── duts/dut-rpi-dutctl-tester/
│   ├── dut.yaml            device definition
│   ├── pre.yaml            setup applied to every test on this DUT
│   └── post.yaml           tear-down applied to every test on this DUT
└── workflows/workflow-rpi-dutctl-tester/
    ├── workflow.yaml
    └── tests/              one test file per dutctl module, three for serial:
        ├── serial.yaml               monitor mode
        ├── serial-interactive.yaml   interactive console (-i)
        └── serial-reconnect.yaml     device unplugged and replugged mid-session
```

### Taskfile
`.firmwareci/Taskfile.yml` automates a few repetitive jobs:

- `misc:dummy-binary-for-flashing` builds the dummy image the flash test writes
- `test:read-out-serial` reads the serial console through dutctl
- `fwci:validate` validates the FirmwareCI files

### duts
The DUT definition and the pre/post stages every test on it inherits. Values the
tests reference (`[[attributes.DeviceName]]`, `[[attributes.DutagentEndpoint]]`,
`[[attributes.Host]]`) come from `dut.yaml`, so a test never hardcodes a
hostname or port.

### workflows
The `workflows` directory is where the tests live. Every module has one test
file, except the serial module, whose tests are split across three test files:

- `serial.yaml` streams the console in monitor mode.
- `serial-interactive.yaml` drives `-i`, both piped and on a real terminal
  (raw mode, Ctrl-C forwarding, the Ctrl-A x quit).
- `serial-reconnect.yaml` unplugs and replugs the fake serial during expect
  and interactive sessions.


## Adding a new test

Create a new `.yaml` file in `.firmwareci/workflows/workflow-rpi-dutctl-tester/tests/`.
Copying an existing test is the quickest start; `dummy.yaml` is a simple one.

A test needs only `name`, `description` and `stages` — the setup and tear-down
come from the DUT. Refer to the device through `[[attributes.*]]` rather than
writing the hostname or port into the test.

Then run `task fwci:validate` before committing.
