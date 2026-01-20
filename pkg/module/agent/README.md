The _agent_ package is a collection of the following dutagent modules:

- [Status](#Status)

# Status

The Status module prints information about the system on which the dutagent itself
is running. 

## Configuration Options

_none_

# Examples
The agent-status module can be used in a command like so. 

``` yaml
cmds:
      system-info:
        desc: "This simple command reports information about the dutagent system via the agent-status module."
        uses:
          - module: agent-status
            main: true
```
See [here](../../../contrib/dutagent-cfg-example.yaml) for a complete dutagent configuration example.