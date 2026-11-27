# DUT Agent Configuration

The _DUT Agent_ is configured by a YAML configuration file: the devices connected to the agent, the commands of each
device, and the modules each command runs. [About the architecture](./architecture.md#devices-commands-and-modules)
explains how commands are built from modules.

[Let a command take arguments](./command-arg-templating.md) shows how to configure the arguments of a command.

## DUT Agent Configuration Schema

| Attribute | Type                          | Default | Description                                                      | Mandatory |
|-----------|-------------------------------|---------|------------------------------------------------------------------|-----------|
| version   | string                        |         | Version of this config schema. dutagent does not check it.       | no        |
| devices   | map[string] [Device](#device) |         | Devices-under-test (DUTs) connected to this agent, keyed by name | yes       |

### Device

| Attribute | Type                             | Default | Description                                                                                                      | Mandatory |
|-----------|----------------------------------|---------|------------------------------------------------------------------------------------------------------------------|-----------|
| desc      | string                           |         | Device description. May be used to state technical details which are important when working with this DUT.       | no        |
| cmds      | map[string] [Command](#commands) |         | Available device commands, keyed by name. Commands are the high level tasks that can be performed on the device. | yes       |

### Commands

| Attribute   | Type                 | Default | Description                                                                                                                                                                                                                                                                                                                                                                                                                                            | Mandatory |
|-------------|----------------------|---------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| desc        | string               |         | Command description                                                                                                                                                                                                                                                                                                                                                                                                                                    | no        |
| uses     | [] [Module](#module) |         | A command may be composed of multiple steps to achieve its purpose. The list of modules represent these steps. The order of this list is important. At most one module may be set as the passthrough module. If a passthrough module is present, all arguments to the command are passed to it, and its usage information is used as the command help text. | yes       |
| args        | [][Argument](#command-arguments) |         | Named arguments that can be passed to the command at runtime and distributed to modules via template syntax. Arguments are mapped positionally in declaration order. **Cannot be used with passthrough modules** - use one or the other.                                                                                                                                                         | no        |

### Command Arguments

Command arguments define named parameters that can be passed at runtime and distributed to modules via template syntax.

A command hands arguments to its modules in exactly one of three ways:

- **Fixed**: the modules have `args`, and the command neither declares `args` nor has a passthrough module. The command
  takes no arguments.
- **Passthrough**: one module has `passthrough: true`, and the command declares no `args`. All runtime arguments go to
  that module, and its usage information is the command's help text. A command has at most one passthrough module.
- **Templated**: the command declares `args`. A module's `args` may contain `${name}` for a declared name, as the whole
  argument or within a longer one, such as `/configs/${name}.yaml`. The runtime arguments are matched to the declared
  ones by position, in the order of the declaration, not by name.

A command cannot have both declared `args` and a passthrough module.

| Attribute | Type   | Default | Description                          | Mandatory |
|-----------|--------|---------|--------------------------------------|-----------|
| name      | string |         | Argument name (used in templates)    | yes       |
| desc      | string |         | Human-readable argument description  | no        |

### Module

| Attribute | Type           | Default | Description                                                                                                                                                                                        | Mandatory                         |
|-----------|----------------|---------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------------------|
| module    | string         |         | ID of the module to use, for example `gpio-switch`.                                                                                                                                                | yes                               |
| passthrough      | bool           | false   | Marks this module as the passthrough module. All runtime arguments to a command are passed to its passthrough module. The passthrough module's usage information is also used as the command help text. | 0 or 1 times per command          |
| args      | []string       | nil     | Fixed arguments of a module that is **not** a command's passthrough module. They may reference declared command arguments as `${name}`, which are replaced with the runtime values. | no, only applies if `passthrough` is not set |
| with   | map[string]any |         | A module can be configured via key-value pairs. The type of the value is generic and depends on the implementation of the module.                                                                  | no, depends on the module         |

> [!IMPORTANT]  
> Refer to `with` keys of a module in all-lowercase representation of the module's exported fields.
> See the respective module's documentation for details.

### Example config file

[demo-example-cfg.yml](../pkg/module/demo/demo-example-cfg.yml), the configuration of the demo modules: commands of
one module each on the device `agent`, and commands of several modules, with fixed, declared and passed through
arguments and module options, on the simulated `demo-board`.

<!-- embed: pkg/module/demo/demo-example-cfg.yml -->
