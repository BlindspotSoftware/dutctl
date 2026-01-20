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
could look like.

### DUT Agent Configuration Schema

| Attribute | Type                 | Default | Description                                             | Mandatory |
|-----------|----------------------|---------|---------------------------------------------------------|-----------|
| version   | string               |         | Version of this config schema                           | yes       |
| devices   | [] [Device](#Device) |         | List of dives-under-test (DUTs) connected to this agent | yes       |

### Device

| Attribute   | Type                    | Default | Description                                                                                                | Mandatory |
|-------------|-------------------------|---------|------------------------------------------------------------------------------------------------------------|-----------|
| description | string                  |         | Device description. May be used to state technical details which are important when working with this DUT. | no        |
| commands    | [] [Command](#Commands) |         | List of available device commands. Commands are the hig level tasks that can be performed on the device.   | no        |

### Commands

| Attribute   | Type                 | Default | Description                                                                                                                                                                                                                                                                                                                                                                                                                                            | Mandatory |
|-------------|----------------------|---------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| description | string               |         | Command description                                                                                                                                                                                                                                                                                                                                                                                                                                    | no        |
| uses     | [] [Module](#Module) |         | A command may be composed of multiple steps to achieve its purpose. The list of modules represent these steps.The order of this list is important, though. Exactly one of the modules must be set as the main module. All arguments to a command are passed to its main module. The main modules usage information is also used as the command help text. If a Command is composed of only one module, this module becomes the main module implicitly. | yes       |

### Module

| Attribute | Type           | Default | Description                                                                                                                                                                                        | Mandatory                         |
|-----------|----------------|---------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------------------|
| module    | string         |         | The module's name also serves as its identifier and must be unique.                                                                                                                                | yes                               |
| main      | bool           | false   | All arguments to a command are passed to its main module. The main modules usage information is also used as the command help text. Can be omitted, if only one modules exists within the command. | exactly once per command          |
| args      | []string       | nil     | If a module is **not** an commands main module, it does not get any arguments passed at runtime, instead arguments can be passed here.                                                             | no, only applies if `main` is set |
| with   | map[string]any |         | A module can be configured via key-value pairs. The type of the value is generic and depends on the implementation of the module.                                                                  | yes                               |

> [!IMPORTANT]  
> Refer to `with` keys in all-lowercase representation of the module's exported fields.
> See the respective module's documentation for details.

### Example config file

See [dutagent-cfg-example.yaml](../contrib/dutagent-cfg-example.yaml)