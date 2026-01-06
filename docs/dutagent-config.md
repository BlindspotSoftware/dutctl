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

| Attribute   | Type                 | Default | Description                                                                                                                                                                                                                                                                                                  | Mandatory |
|-------------|----------------------|---------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| description | string               |         | Command description                                                                                                                                                                                                                                                                                          | no        |
| args        | map[string]string    |         | Named arguments that can be passed to the command. Keys are argument names, values are descriptions. When a command is invoked with positional arguments, they are mapped to these named arguments in alphabetical order by key. Arguments can be referenced in module args using `${argname}` syntax.       | no        |
| modules     | [] [Module](#Module) |         | A command may be composed of multiple steps to achieve its purpose. The list of modules represent these steps. The order of this list is important. All modules contribute their help text to the command's documentation. Modules can reference command arguments using template syntax in their args field. | yes       |

### Module

| Attribute | Type           | Default | Description                                                                                                                                                                                                                                                                                                              | Mandatory |
|-----------|----------------|---------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|-----------|----------------|---------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|-----------|----------------|---------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|-----------|----------------|---------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| module    | string         |         | The module's name also serves as its identifier and must be unique.                                                                                                                                                                                                                                                      | yes       |
| args      | []string       | nil     | Arguments to pass to the module. Can reference command-level arguments using template syntax `${argname}`. For example, if a command defines `args: {firmware: "firmware image path"}`, a module can use `args: ["${firmware}"]` to receive that argument. Static values can also be provided without templates. | no        |
| options   | map[string]any |         | A module can be configured via key-value pairs. The type of the value is generic and depends on the implementation of the module.                                                                                                                                                                                        | yes       |

> [!IMPORTANT]  
> Refer to option keys of a module in all-lowercase representation of the modules exported fields.
> See the respective module's documentation for details.

### Argument Templating

Commands can define named arguments that are passed to modules using template syntax. This allows reusing the same argument value across multiple modules in a multi-step command.

**How it works:**
1. Define arguments at the command level with the `args` field (a map of argument names to descriptions)
2. Reference these arguments in module `args` using `${argname}` syntax
3. When invoking a command with positional arguments, they are mapped to the defined argument names in alphabetical order

**Example:**
```yaml
commands:
  flash-and-verify:
    desc: "Flash firmware and verify it"
    args:
      firmware: "Path to firmware image file"
      output: "Path to save verified image"
    modules:
      - module: flash
        args: ["write", "${firmware}"]
      - module: flash
        args: ["read", "${output}"]
```

When called with: `dutctl run device1 flash-and-verify image.rom verified.rom`
- `image.rom` → `firmware` (first alphabetically)
- `verified.rom` → `output` (second alphabetically)
- Both modules receive the expanded values

### Example config file

See [dutagent-cfg-example.yaml](../contrib/dutagent-cfg-example.yaml)