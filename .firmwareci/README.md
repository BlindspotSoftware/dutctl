# FirmwareCI configuration for dutctl testing

This directory contains FirmwareCI configurations and tests.

For detailed information on FirmwareCI, please refer to the [official documentation](https://docs.firmware-ci.com/).


## Requirements

- Parametric and re-usable testing
- Easy to expand
- Reliable
- Can be used in CI/CD to verify each pull request


## Design decisions

### Jinja templating
We want to test multiple features (flashing, power control, serial, etc). And to keep everything nicely organised we need to have each feature-specific test as it's own test.

However, the setup and tear-down of the tests is rather complex:
- copy over the compiled binaries
- copy over configuration files
- spin up the server (agent)
- execute the test
- shut-down the server
- delete the copied binaries
- delete configuration files

In case of the serial feature, it also means spinning-up "fake" / "dummy" serial that can be used for the testing.

It would be stupid to copy-paste all this setup into each test file, especially because keeping all of them in sync would be a nightmare.

For this reason I have decided to use [jinja2 templates](https://jinja.palletsprojects.com/en/stable/api/) to automate this menial and error-prone work. To make it even simpler, I have also included [Taskfile](https://taskfile.dev/docs/guide) which has already all the jobs written there, so to generate all of the templates is as easy as simply calling `task jinja2:templates`.

Thanks to jinja templating, and how the tests are structured, tests are parametric and easy to expand.


### Debian packaging magic
As you might notice, instead of simply copying over the compiled binaries (`dutctl` / `dutagent` / ...) we use Debian packages.

The biggest motivator here to do this was to accommodate for setup and tear-down, and the differences between tested versions (future-proofing). This way, we leverage the power of package manager to make sure that the cleanup (uninstalling) of old file is complete and that no files are left behind! This makes sure that the environment is always pristine.

Another huge advantage is, that with this approach, that run-time dependencies are also handled by package manager. Because of this, the test hardware (Raspberry Pi) has minimal (if any) setup required for use in the FWCI tests. Just install package, test and uninstall! Easy!


## Project structure

The most important files / directories are:
- `.firmwareci/Taskfile.yml`
- `.firmwareci/.jinja2_templates/`
- `.firmwareci/workflows/`

### Taskfile
The `Taskfile.yml` is there to help and automate some mundane and repetitive jobs.


### jinja2_templates
The `.jinja2_templates` contains the "common" building-blocks that are shared across templates. The `defaults.j2` and `defaults.yaml` are for storing some basics.

The `pre.yaml.j2` and `post.yaml.j2` are for pre-stage and post-stage respectively. They are copy-pasted into each test.

### workflows
The `workflows` directory is where the tests live.


## Adding completely new test
To add a completely new tests from scratch, create a new `.yaml.j2` file in `.firmwareci/workflows/workflow-rpi-dutctl-tester/`. I would recommend to copy-paste some existing test (for example the `dummy-modules.yaml.j2` is rather simple) and use it as basis.

Then update the test according to your needs.

Run `task fwci:jinja2:templates` and it will automatically generate a complete tests. That is it. It is as simple as that.

Next, I recommend to run `fwci fwci:validate`.

As the last step, add all new files (even those jinja2 generated) into git commit. This is because FirmwareCI cannot pre-process the tests on it's own, which is why we have to include even the "generated code".
