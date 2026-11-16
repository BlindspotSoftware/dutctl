The _gpio_ package is a collection of the following dutagent modules:

- [Button](#Button)
- [Switch](#Switch)
- [Sensor](#Sensor)

The modules are designed to use different ways to interact with GPIO. At the moment only one such _backend_ is supported
and set by default. That is memory mapping `dev/mem` memory region and manipulating GPIO registers by writing to that
memory. 

> [!IMPORTANT]  
> It is the user's responsibility to ensure that the used GPIO pin is not also used by other modules
> or otherwise occupied by the system.

# Button

This module simulates a button press by changing the state of a GPIO pin.

```
ARGUMENTS:
	[duration]

The duration controls the time the button is pressed. If no duration is passed, a default is used.

A duration string is a possibly signed sequence of decimal numbers, each with optional fraction
and a unit suffix, such as "300ms", "-1.5h" or "2h45m".
Valid time units are "ns", "us" (or "µs"), "ms", "s", "m", "h".`
```

See [gpio-example-cfg.yml](./gpio-example-cfg.yml) for examples. 

## Configuration Options

| Option    | Value  | Description                                                             |
|-----------|--------|-------------------------------------------------------------------------|
| pin       | int    | Raw BCM2835/BCM2711 pin number                                          |
| activelow | bool   | If true, the idle state is high, and low when pressed. Default is false |
| backend   | string | For future use. Name of the backend to use. Default is "devmem"         |
| label     | string | Optional tag for the button's purpose or connected item, e.g. "Host reset line". Messages then read "Host reset line (GPIO 9): pressed for 500ms" instead of "GPIO 9: pressed for 500ms". |

# Switch

This module simulates an on/off switch by changing the state of a GPIO pin.

```
ARGUMENTS:
	[on|off|toggle]

The on, off and toggle commands control the state of the switch.
If no argument is passed, the current state is printed.
```

The Switch is initially turned off and by default off means _low_ and on means _high_.

See [gpio-example-cfg.yml](./gpio-example-cfg.yml) for examples.

## Configuration Options

| Option    | Value  | Description                                                                                   |
|-----------|--------|-----------------------------------------------------------------------------------------------|
| pin       | int    | Raw BCM2835/BCM2711 pin number                                                                |
| initial   | string | Initial state of the switch: "on" or "off" (case insensitive). Default and fallback is "off". |
| activelow | bool   | If true, the switch is active low (switch on means gpio pin low). Default is false.           |
| backend   | string | For future use. Name of the backend to use. Default is "devmem"                               |
| label     | string | Optional tag for the switch's purpose or connected item, e.g. "Green status LED". Messages then read "Green status LED (GPIO 17): turned on" instead of "GPIO 17: turned on". |

# Sensor

This module reads a digital input: the level of a GPIO pin that the DUT drives, such as a power-good signal,
a "boot done" line or a status LED. The pin is configured as an input and is never driven by the module.

```
ARGUMENTS:
	none

Prints whether the signal is active or inactive, followed by the pin's level.
```

For example: `Power good (GPIO 22): active (high)`.

By default active means _high_. Without an internal pull resistor, a pin that nothing drives floats and reads
random levels; set `pull` if the DUT's signal can be left undriven, for example an open-drain output.

See [gpio-example-cfg.yml](./gpio-example-cfg.yml) for examples.

## Configuration Options

| Option    | Value  | Description                                                                                                   |
|-----------|--------|---------------------------------------------------------------------------------------------------------------|
| pin       | int    | Raw BCM2835/BCM2711 pin number, 0 to 57. Larger values are an error.                                          |
| activelow | bool   | If true, the signal is active low (pin low means active). Default is false.                                   |
| pull      | string | Internal pull resistor: "up", "down" or "off" (case insensitive). Default is "off". Other values are an error. |
| backend   | string | For future use. Name of the backend to use. Default is "devmem"                                               |
| label     | string | Optional tag for the sensor's purpose or connected item, e.g. "Power good". Messages then read "Power good (GPIO 22): active (high)" instead of "GPIO 22: active (high)". |
