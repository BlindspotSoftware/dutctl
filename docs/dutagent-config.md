# DUT Agent Configuration

The _DUT Agent_ is configured by a YAML configuration file.

The configuration mainly consists of a list of devices connected to an agent and a list of the _Commands_ available
for those devices. Commands are meant to be the high-level tasks you want to perform on the device, e.g.
"Flash the firmware with the given image." To achieve this high-level task, commands can be built up of one or multiple
_Modules_. Modules represent the basic operations and represent the actual implementation for the hardware interaction.
The implementation of a Module determines its capabilities and also exposes information on how to use and configure it.

The DUT Control project offers a collection of Module implementations but also allows for easy integration of [custom modules](./module_guide.md).
Often a _Command_ can consist of only one _Module_ to get the job done, e.g., power cycles the device. But in some cases
like the flash example mentioned earlier, eventually it is mandatory to toggle some GPIOs before doing the actual SPI flash
operation. In this case the command is built up of a Module dealing with GPIO manipulation and a Module performing a
flash writing with a specific programmer. See the second device in the [example](#example-config-file) down below on what this
could look like. Commands support static module args, `forwardArgs` modules that receive runtime arguments, and command-level argument templating that distributes named arguments to non-forwardArgs modules via template syntax. These can be combined: see [Command Argument Templating](./command-arg-templating.md) for details.

## DUT Agent Configuration Schema

| Attribute | Type                 | Default | Description                                             | Mandatory |
|-----------|----------------------|---------|---------------------------------------------------------|-----------|
| version   | string               |         | Version of this config schema                           | yes       |
| devices   | [] [Device](#device) |         | List of devices-under-test (DUTs) connected to this agent | yes       |

### Device

| Attribute   | Type                    | Default | Description                                                                                                | Mandatory |
|-------------|-------------------------|---------|------------------------------------------------------------------------------------------------------------|-----------|
| description | string                  |         | Device description. May be used to state technical details which are important when working with this DUT. | no        |
| commands    | [] [Command](#commands) |         | List of available device commands. Commands are the high level tasks that can be performed on the device.   | no        |

### Commands

| Attribute   | Type                 | Default | Description                                                                                                                                                                                                                                                                                                                                                                                                                                            | Mandatory |
|-------------|----------------------|---------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| description | string               |         | Command description                                                                                                                                                                     | no        |
| uses     | [] [Module](#Module) |         | A command may be composed of multiple steps to achieve its purpose. The list of modules represent these steps. The order of this list is important. At most one module may be set as the forwardArgs module. If a forwardArgs module is present, remaining runtime arguments (those not consumed by command-level template substitution) are passed to it, and its usage information is used as the command help text. | yes       |
| args        | [][Argument](#command-arguments) |         | Named arguments that can be passed to the command at runtime and distributed to non-forwardArgs modules via template syntax. Arguments are mapped positionally in declaration order. Can be combined with `forwardArgs`: the first N runtime args are consumed for substitution, any remaining args are forwarded to the `forwardArgs` module. | no        |

### Command Arguments

Command arguments define named parameters that can be passed at runtime and distributed to modules via template syntax.

| Attribute | Type   | Default | Description                          | Mandatory |
|-----------|--------|---------|--------------------------------------|-----------|
| name      | string |         | Argument name (used in templates)    | yes       |
| desc      | string |         | Human-readable argument description  | yes       |

### Module

| Attribute | Type           | Default | Description                                                                                                                                                                                        | Mandatory                         |
|-----------|----------------|---------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------------------|
| module    | string         |         | The module's name also serves as its identifier and must be unique.                                                                                                                                | yes                               |
| forwardArgs      | bool           | false   | Marks this module as the forwardArgs module. Remaining runtime arguments (those not consumed by command-level template substitution) are passed to it. The forwardArgs module's usage information is also used as the command help text. | 0 or 1 times per command |
| args      | []string       | nil     | Static arguments passed to this module at runtime. Only applies to non-forwardArgs modules.                                                                                                              | no                       |
| with   | map[string]any |         | A module can be configured via key-value pairs. The type of the value is generic and depends on the implementation of the module.                                                                  | yes                               |

> [!IMPORTANT]  
> Refer to `with` keys of a module in all-lowercase representation of the module's exported fields.
> See the respective module's documentation for details.

### Example config file

See [dutagent-cfg-example.yaml](../contrib/dutagent-cfg-example.yaml)