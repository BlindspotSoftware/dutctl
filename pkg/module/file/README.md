The _file_ package provides a single module:

# File

This module transfers files between client and dutagent, supporting both upload and download operations.

```
ARGUMENTS:
	<path>              Use destination from config, or working directory if not set
	<source>:<dest>     Explicitly specify both source and destination

The operation type (upload or download) must be configured in the device YAML.

For upload operations:
  - <source> is the filepath on the client
  - <destination> is the filepath on the dutagent where the file will be saved

For download operations:
  - <source> is the filepath on the dutagent
  - <destination> is the filepath on the client where the file will be saved

IMPORTANT: The colon syntax (<source>:<dest>) and destination config option are
mutually exclusive. Using both will result in an error.

Destination resolution:
  1. destination (if configured) - Colon syntax cannot be used
  2. Colon syntax (if provided and no destination configured)
  3. Working directory + basename (fallback)
```

See [file-example-cfg.yml](./file-example-cfg.yml) for examples.

## Configuration Options

| Option      | Value  | Required | Description                                                                                        |
|-------------|--------|----------|----------------------------------------------------------------------------------------------------|
| operation   | string | yes      | Operation type: "upload" or "download"                                                             |
| destination | string | no       | Destination path on either dutagent or client. Overrides any other destination information         |
| permission  | string | no       | File permissions in octal format (e.g., "0644", "0755"). Upload only. Default: "0644"              |

