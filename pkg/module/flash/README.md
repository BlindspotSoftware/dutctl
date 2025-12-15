The _flash_ package provides a single module:

# Flash

Read or write the SPI flash on the DUT.

```
ARGUMENTS:
	[read | write] <image>

For read operation, <image> sets the filepath the read image is saved at the client.
For write operation, <image> is the local filepath to the image at the client.

```

This module is a wrapper around a flasher tool on the _dutagent_. Supported tools: _flashrom_, _flashprog_.
The flasher tool must be installed on the _dutagent_, and suitable flasher hardware must be hooked up to the DUT.
Functionality is tested with DediProg programmers.

See [flash-example-cfg.yml](./flash-example-cfg.yml) for examples.

## Configuration Options

| Option     | Value  | Description                                                                                       |
|------------|--------|---------------------------------------------------------------------------------------------------|
| programmer | string | Name of the flasher hardware, that is used by the _dutagent_ and attached to the DUT's SPI flash. |
| tool       | string | Path to the flasher tool binary on the _dutagent_. Defaults to `/bin/flashrom` if not specified. Supported: flashrom, flashprog. |
