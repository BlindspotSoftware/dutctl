# Agent status

The module `agent-status` prints information about the system on which the dutagent itself is running, the output of
`uname -a`.

## Arguments

None; arguments are ignored.

## Configuration Options

None.

## Example

A command that reports the agent's system:

```yaml
cmds:
  system-info:
    desc: "Report information about the dutagent system."
    uses:
      - module: agent-status
        passthrough: true
```

See [demo-example-cfg.yml](../demo/demo-example-cfg.yml) for a complete dutagent configuration.
