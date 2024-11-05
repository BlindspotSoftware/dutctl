The _agent_ package is a collection of the following dutagent modules:

- [Wait](#Wait)

# Wait

This module waits for a specific amount of time. 

```
SYNOPSIS:
    time-wait [duration]

A duration string is a possibly signed sequence of decimal numbers, each with optional fraction
and a unit suffix, such as "300ms", "-1.5h" or "2h45m".
Valid time units are "ns", "us" (or "µs"), "ms", "s", "m", "h".`
```

If no duration is passed the module waits for a default duration. This is either set via
the module's configuration or falls back to an internal default. The value passed via the
command line overrides the configured duration, the configured duration override the internal
default value. 

See [time-example-cfg.yml](./time-example-cfg.yml) for examples. 

## Configuration Options


| Option | Value | Description
|----------|--------|------------------------------------|
| duration | string | See description of cmdline argument

