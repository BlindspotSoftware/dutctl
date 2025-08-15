The _shell_ package provides a single module:

# Flash

Read or write the SPI flash on the DUT.

```
ARGUMENTS:
	[read | write] <image>

For read operation, <image> sets the filepath the read image is saved at the client. 
For write operation, <image> is the local filepath to the image at the client.

```

This module is a wrapper around flash-software on the _dutagent_. At the moment only _flashrom_ is supported.
Other software will be configurable in this module later. The flasher software must be installed on the _dutagent_, and
 suitable flasher hardware must be hooked up to the DUT. Functionality is tested with DediProg programmers.

See [flash-example-cfg.yml](./flash-example-cfg.yml) for examples. 

## Configuration Options

| Option     | Value  | Description                                                                                       |
|------------|--------|---------------------------------------------------------------------------------------------------|
| programmer | string | Name of the flasher hardware, that is used by the _dutagent_ and attached to the DUT's SPI flash. |
