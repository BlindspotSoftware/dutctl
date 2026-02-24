The _flash-emulate_ package provides a single module:

# FlashEmulate

Load a firmware image into an SPI flash emulator on the DUT.

```
ARGUMENTS:
	<image>

<image> is the local filepath to the firmware image at the client.

```

The image is transferred to the _dutagent_ and loaded into the SPI flash emulator.
Any running emulation session is stopped first, then the new image is loaded and emulation is started.

This module wraps an emulation tool on the _dutagent_ (default: `em100`). The tool must be installed on the _dutagent_,
and a compatible emulator (e.g. EM100Pro-G2 from Dediprog) must be connected to the DUT's SPI flash bus.

See [flash-emulate-example-cfg.yml](./flash-emulate-example-cfg.yml) for examples.

## Configuration Options

| Option | Value  | Required | Description                                                                                                         |
|--------|--------|----------|---------------------------------------------------------------------------------------------------------------------|
| chip   | string | yes      | SPI flash chip type identifier (e.g. `N25Q256A13`). See the `em100` documentation for the list of supported chips. |
| tool   | string | no       | Path to the emulation tool binary on the _dutagent_. Defaults to `em100`.                                          |
| device | string | no       | USB device number to select a specific emulator when multiple are connected.                                        |
