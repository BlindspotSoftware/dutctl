The _flash_ package provides a single module:

# Flash

Read or write the SPI flash on the DUT.

```
ARGUMENTS:
	[read | write] <image>

For read operation, <image> sets the filepath the read image is saved at the client.
For write operation, <image> is the local filepath to the image at the client.

```

This module is a wrapper around a flasher tool on the _dutagent_. Supported tools: _flashrom_, _flashprog_, _dpcmd_.
The flasher tool must be installed on the _dutagent_, and suitable flasher hardware must be hooked up to the DUT.
Functionality is tested with DediProg programmers.

See [flash-example-cfg.yml](./flash-example-cfg.yml) for examples.

## Configuration Options

| Option     | Value  | Description                                                                                       |
|------------|--------|---------------------------------------------------------------------------------------------------|
| tool       | string | Path to the flasher tool binary on the _dutagent_. Supported: flashrom, flashprog, dpcmd. |
| programmer | string | Specifics of the flasher hardware. For flashrom/flashprog: programmer name (e.g., "dediprog"). For dpcmd: (Optional) USB device number. Required for flashrom/flashprog, optional for dpcmd. See the respective flash-tool documentation for supported values. |
