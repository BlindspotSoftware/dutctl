The _file_ package provides a single module:

# File

This module transfers files between client and dutagent, supporting both upload and download operations.

```
ARGUMENTS:
	<path>              Single path argument. Interpretation depends on configuration.
	<source>:<dest>     Explicitly specify both paths (incompatible with source/destination config)

The operation type (upload or download) must be configured in the device YAML.

Path Restrictions:
  - Destination paths (sanitized): must be relative, no absolute paths, no leading '..'
  - Source paths (not sanitized): any path allowed, including absolute paths

Configuration Behavior:
  - source configured: arg becomes destination (sanitized - must be relative)
  - destination configured: arg becomes source (not sanitized - absolute allowed)
  - neither configured: arg becomes both source and destination (dest is sanitized)
  - both configured: no arg allowed
  - Colon syntax: both parts are destinations and sanitized (must be relative)

Path Processing:
  - Relative paths preserve directory structure: ./dir/file.bin → dir/file.bin
  - Internal '..' references are resolved: dir/../other/file.bin → other/file.bin
```

## Examples

The commands below refer to the configuration in
[file-example-cfg.yml](./file-example-cfg.yml). `$WD` is a placeholder for the
working directory on the client and the dutagent respectively.

```
# No paths configured: the argument is source and destination.
dutctl example-device upload ./build/firmware.bin
→ uploads to $WD/firmware.bin (preserves directory structure)

dutctl example-device upload ./firmware.bin:custom/firmware.bin
→ uploads to $WD/custom/firmware.bin (custom destination via colon syntax)

# Destination configured: the argument becomes the source.
dutctl example-device upload-script ./scripts/init.sh
→ uploads to /opt/scripts/install.sh on the device

# Source configured: the argument becomes the destination.
dutctl example-device fetch-logs ./local-log.txt
→ downloads /var/log.txt to ./local-log.txt on the client

# Both paths configured: no argument allowed.
dutctl example-device upload-config
→ uploads ./configs/device.conf to /etc/device/device.conf
```

## Configuration Options

| Option      | Value  | Required | Description                                                                                        |
|-------------|--------|----------|----------------------------------------------------------------------------------------------------|
| operation   | string | yes      | Operation type: "upload" or "download"                                                             |
| source      | string | no       | Source path. If set, argument path becomes the destination. Cannot be used with colon syntax       |
| destination | string | no       | Destination path. If set, argument path becomes the source. Cannot be used with colon syntax       |
| permission  | string | no       | File permissions in octal format (e.g., "0644", "0755"). Upload only. Default: "0644"              |
