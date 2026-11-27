# Wait

The module `time-wait` waits for a specific amount of time.

## Arguments

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

## Configuration Options

| Option   | Type   | Description                          |
|----------|--------|--------------------------------------|
| duration | string | See the description of the argument. |

## Example

See [time-example-cfg.yml](./time-example-cfg.yml).

