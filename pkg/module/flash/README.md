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
| skipUnchanged | object | Optional, flashrom/flashprog only. Write only the regions that change at runtime when the chip already holds the image. See below. |
| recover | object | Optional, flashrom/flashprog only. Power-cycle the programmer's USB hubs when it does not respond or a write fails, then write the whole chip again. See below. |

A faster SPI clock goes into the programmer string, for example `dediprog:spispeed=24M`. Every write reads the whole chip before it writes and again to verify, so the clock dominates the time of a write.

### skipUnchanged

After a full write that the flash tool verified, the module stores the SHA-256 of the image. When the next write uses the same image, the module first compares the first 4 KiB of every skipped region on the chip with the image. If they match, it writes only the regions in `always`, with `-l <layout> -i <region> -N -w`. Otherwise it writes the whole chip.

The stored hash is deleted before every full write and written again only after the tool reports success. A write that fails or is cancelled therefore always leads to a full write next time.

| Option | Value | Description |
|--------|-------|-------------|
| preset | string | Built-in layout. `openbmc-static`: the static OpenBMC layouts of 64 MiB and 128 MiB images (`openbmc-flash-layout-64.dtsi`, `-128.dtsi`), chosen by image size, with `always: [u-boot-env, rwfs]`. |
| regions | list | Explicit layout instead of a preset: `name`, `start`, `end` (inclusive, hex or decimal). Regions must not overlap. |
| always | list | Regions written on every write: the ones the firmware writes at runtime. Each must be in the layout, and at least one region must be left to skip. |
| state | string | File for the stored hash, relative to the _dutagent_ working directory. Default: `flash-skip-<hash of programmer>.sha256`. |

### recover

Before a write, the module asks the flash tool to identify the chip. If that fails, or if a write fails, the module:

1. switches off all ports of every hub in `hubs` with `uhubctl -l <hub> -a off`,
2. waits `off` and switches the ports on again (also when the job is cancelled),
3. waits until `device` appears on USB and answers the flash tool,
4. writes the whole chip.

A short power cut is not enough to reset a hung DediProg. On boards that gang the power of several ports, such as the Raspberry Pi 4, VBUS drops only when all ports of all hubs of the gang are off (`1-1` and `2` on a Pi 4). The kernel must also be kept from powering a port again: the package installs a udev rule that lets the `plugdev` group write the port `disable` files, which `uhubctl` then uses (see [packaging](../../../packaging/README.md)). The module prints a warning when the programmer is still on USB halfway through the cut.

| Option | Value | Description |
|--------|-------|-------------|
| hubs | list | uhubctl locations whose ports are all switched off, for example `["1-1", "2"]`. Required. |
| device | string | USB vendor:product ID of the programmer, for example `0483:dada` for a DediProg SF600PG2. Required. |
| off | duration | How long the ports stay off. Default `15s`. |
| timeout | duration | How long to wait for the programmer to answer after the cut. Default `60s`. |

