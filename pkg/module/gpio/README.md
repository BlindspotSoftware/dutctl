The _gpio_ package is a collection of the following dutagent modules:

- [Button](#Button)
- [Switch](#Switch)

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