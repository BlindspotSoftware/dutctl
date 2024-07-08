# [DRAFT] DUT Agent Configuration

The _DUT Agent_ is configured by a YAML configuration file.

The configuration mainly consists of a list of devices connected to an agent and and a list of the _Commands_ available
for those devices. Commands are meant to be the hig level tasks you want to perform on the device e.g 
"Flash the firmware with the given image". To achieve this high level task, commands can be build up of one or multiple
_Modules_. Modules represent the basic operations and represent the actual implementation for the hardware interaction.
The implementation of a Module determines its capabilities and also exposes information on how to use und configure it.  

The DUT Control project offers a collection of Module implementation but also allows for easy integration of [custom modules](./module_guide.md). 
Often a _Command_ can consist of only one _Module_ to get the job done, e.g. power cycle the device. But in some cases
like the flsh example mentioned earlier, eventually it is mandetory to toggle some GPIOs befor doing the actual SPI flash
operation. In this case the command is build up of a Module dealing with GPIO manipulation and a Module performing a
flash write with a specific programmer. See the 2nd device in the [example](#example-config-file) down below on how this
could look like.

### DUT Agent Configuration Schema

| Attribute | Type | Default | Description | Mandatory |
| --- | --- | --- | --- | --- |
| version | string |  | Version of this config schema | yes |
| devices | [] [Device](#Device) |  | List of dives-under-test (DUTs) connected to this agent | yes |

### Device

| Attribute | Type | Default | Description | Mandatory |
| --- | --- | --- | --- | --- |
| description | string |  | Device description. May be used to state technical details which are important when working with this DUT. | no |
| commands | [] [Command](#Commands) |  | List of available device commands. Commands are the hig level tasks that can be performed on the device. | no |

### Commands

| Attribute | Type | Default | Description | Mandatory |
| --- | --- | --- | --- | --- |
| description | string |  | Command description | no |
| Modules | [] [Module](#Module) |  | A command may be composed of multiple steps to achieve its purpose. The list of modules represent these steps.The order of this list is important, though. Exactly one of the modules must be set as the main module. All arguments to a command are passed to its main module. The main modules usage information is also used as the command help text.| yes |

### Module

| Attribute | Type | Default | Description | Mandatory |
| --- | --- | --- | --- | --- |
| module | string |  | The module's name also serves as its identifier and must be unique. | yes |
| main | bool | false | All arguments to a command are passed to its main module. The main modules usage information is also used as the command help text. | exactly once per command |
| args | string | nil | If a module is not an commands main module, it does not get any arguments passed at runtime, instead arguments can be passed here.| when main is false |
| options | map[string]any |  | A module can be configured via key-value pairs. The type of the value is generic and depends on the implementation of the module.| yes |


### Example config file

```yaml
---
version: 0
devices:
  my-device-1:
    desc: Example device with basic commands
    cmds:
      power:
        desc: press power button
        modules:
          - module: gpio
            options:
              pin: 37
              interface: sysfs
              type: button
              duration: "1s"
              activ-low: true
      reset:
        desc: control reset switch
        modules:
          - module: gpio
            options:
              pin: 38
              interface: sysfs
              type: switch
      serial:
        desc: access host console via ssh
        modules:
          - module: ssh
            options:
              host: foo.example.com
              user: root
              password: deadbeef
      flash:
        desc: flash firmware flashchip
        modules:
          - module: flashrom
            options:
              programmer: "ch341a_spi"
              chip: W25Q512JV

  my-device-2:
    desc: Example device using commands with multiple modules
    cmds:
      power:
        desc: control main power PDU with delay
        modules:
          - module: pdu-intelligent
            main: true
            options:
              url: http://pdu.example.com
              user: user
              password: admin
              outlet: 3
          - module: sleep
            args: ""
            options:
              duration: "3s"
      host-serial:
        desc: access host serial console
        modules:
          - module: gpio
            args: low
            options:
              pin: 10
              interface: gpiomem
              type: switch
          - module: tty-serial
            main: true
            options:
              baud: 115200
      bmc-serial:
        desc: access bmc serial console
        modules:
          - module: gpio
            args: high
            options:
              pin: 10
              interface: gpiomem
              type: switch
          - module: tty-serial
            main: true
            options:
              baud: 9600
      flash:
        desc: program firmware chip
        modules:
          - module: gpio
            args: low
            options:
              pin: 20
              interface: gpiomem
              type: switch
          - module: dpcmd
            main: true
            options:
              write_arg: "--auto"
          - module: gpio
            args: high
            options:
              pin: 20
              interface: gpiomem
              type: switch
```

