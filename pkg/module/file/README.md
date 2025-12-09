The _file_ package provides a single module:

# File

This module transfers files between the client and dutagent.

```
ARGUMENTS:
	[path]
	[source:destination]

The operation type (upload or download) must be configured in the device YAML.

For single path form:
  - If destination is configured: uses configured destination
  - If destination not configured: uses working directory + basename

For colon syntax:
  - Explicitly specifies both source and destination paths
  - Only works if destination is NOT configured
  
For upload: <source> is client path, <destination> is dutagent path
For download: <source> is dutagent path, <destination> is client path
```

See [file-example-cfg.yml](./file-example-cfg.yml) for examples.

## Configuration Options

| Option | Value | Description |
|--------|-------|-------------|
| operation | string | Operation type: "upload" or "download" (required) |
| destination | string | Destination path. Overrides any colon syntax in arguments (optional) |
| forcedir | bool | Force creation of parent directories if they don't exist. Upload only (default: false) |
| overwrite | bool | Overwrite existing destination files. Upload only (default: false) |
| permission | string | File permissions in octal format, e.g., "0644", "0755". Upload only (optional) |
