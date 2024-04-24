# [DRAFT] DUT Agent Configuration

The _DUT Agent_ is configured by a YAML configuration file.

### DUTAgent Schema

| Attribute | Type | Default | Description | Mandatory |
| --- | --- | --- | --- | --- |
| version | string |  | Version of this config schema | yes |
| devices | [Devices](#Device) |  | List of dives-under-test (DUTs) connected to this agent | yes |

### Device

| Attribute | Type | Default | Description | Mandatory |
| --- | --- | --- | --- | --- |
| description | string |  | Device description | no |
| commands | [Commands](#Commands) |  | List of available device commands | no |

### Commands

| Attribute | Type | Default | Description | Mandatory |
| --- | --- | --- | --- | --- |
| description | string |  | Command description | no |
| Modules | [Modules](#Module) |  | A command can be composed of multiple actions to fullfill its purpose. The list of modules represent these steps. The order of this list is important, though. | yes |

### Module

| Attribute | Type | Default | Description | Mandatory |
| --- | --- | --- | --- | --- |
| module | string |  | The module's name is a unique identifier | yes |
| main | bool | false | All arguments to a command are passed to its main module. The main modules usage information is also used as the command help text. | exactly once per command |
| args | string or null (Golang: *string) | nil | If a module is not an commands main module, it does not get any arguments passed at runtime, instead arguments can be passed here.| when main is false |
| options | map[string]any |  | Depending on the respective implementation the behavior of a module can be configured via options.| yes |

**TODO:**

What happens on error case? Should the following modules be executed or skipped?

## Parsing Modules

1. Each command must have a module set to main in modules.
2. If only one module is defined for a command, the main attribute within the module is implicitly set to true.
3. Modules with main set to false must have args defined.
4. main and args are mutually exclusive within a module.

### Example config file

```yaml
---
version: 0
devices:
  example1:
    desc: first example device
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

  example2:
    desc: second example device using commands with multiple modules
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

### Example config parser (GPT4)

```go
package main

import (
        "encoding/json"
        "fmt"
        "log"
        "os"

        "gopkg.in/yaml.v2"
)

type Config struct {
        Version int               `yaml:"version"`
        Devices map[string]Device `yaml:"devices"`
}

type Device struct {
        Description string             `yaml:"desc"`
        Commands    map[string]Command `yaml:"cmds"`
}

type Command struct {
        Description string   `yaml:"desc"`
        Modules     []Module `yaml:"modules"`
}

type Module struct {
        Module  string            `yaml:"module"`
        Main    bool              `yaml:"main,omitempty"`
        Args    *string           `yaml:"args,omitempty"`
        Options map[string]string `yaml:"options"`
}

func main() {
        file, err := os.ReadFile("config.yml")
        if err != nil {
                log.Fatalf("Could not read file: %v", err)
        }

        var config Config
        err = yaml.Unmarshal(file, &config)
        if err != nil {
                log.Fatalf("Could not unmarshal YAML: %v", err)
        }

        // Process configuration according to the following rules:
        // - Each command must have a module set to main in modules.
        // - If only a single module is defined for a command, it is implicitly set as the main module.
        // - Modules where main is set to false must have args defined.
        // - main and args in a module are mutually exclusive.
        for _, dev := range config.Devices {
                for _, cmd := range dev.Commands {
                        var mainCmdCount int
                        for _, md := range cmd.Modules {
                                if md.Main && (md.Args != nil && *md.Args != "") {
                                        log.Fatal("Error: args and main cannot coexist in a module")
                                }
                                if len(cmd.Modules) == 1 {
                                        cmd.Modules[0].Main = true
                                } else {
                                        if md.Main {
                                                mainCmdCount++
                                        }
                                        if md.Args == nil && !md.Main {
                                                log.Fatal("Error: args must be defined for non-main modules")
                                        }
                                }
                        }
                        if mainCmdCount > 1 {
                                log.Fatal("Error: Only one main module can be set for each command")
                        }
                }
        }

        j, err := json.MarshalIndent(config, "", "  ")
        if err != nil {
                log.Fatalf("Could not marshal JSON: %v", err)
        }

        fmt.Print(string(j))
}
```
