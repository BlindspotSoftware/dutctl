# File Module

The `file` module provides file transfer capabilities between the client and dutagent, allowing you to upload files to the device and download files from the device.


## Usage

### Command Syntax

The file module supports two argument forms:

```bash
# Single path
dutctl device cmd <path>

# Colon syntax
dutctl device cmd <source>:<destination>
```

The operation type (upload or download) must be configured in the device YAML.

### Destination Path Resolution Priority

The module determines the final source and destination paths using this priority order:

1. **`default_destination` in config** - If set, always takes precedence
2. **Colon syntax** - If provided (`:` separator), overrides defaults
3. **Working directory + basename** - Fallback if neither above is set

**Examples:**

```bash
# Uses default_destination from config
dutctl device1 upload-firmware ./firmware.bin

# Colon syntax - only works if default_destination is NOT set
dutctl device1 upload-firmware ./firmware.bin:/custom/path/firmware.bin

# If no default_destination, uploads to working-dir/firmware.bin
dutctl device1 upload-firmware ./firmware.bin
```

## Configuration Options

Options are configured in the device YAML configuration:

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `operation` | string | **required** | Operation type: `"upload"` or `"download"` |
| `default_destination` | string | optional | **Takes highest priority.** Destination path on either dutagent or dut-client. If set, colon syntax is ignored. |
| `forcedir` | bool | `false` | Force creation of parent directories if they don't exist (upload only) |
| `overwrite` | bool | `false` | Overwrite existing destination files (upload only) |
| `mode` | string | `""` | File permissions in octal format, e.g., `"0644"`, `"0755"` (upload only) |

## Examples

### Basic Upload

Upload a firmware file with default settings:

```yaml
devices:
  device1:
    cmds:
      upload-firmware:
        desc: "Upload firmware to device"
        modules:
          - module: file
            main: true
            options:
              operation: "upload"
              default_destination: "/tmp/firmware.bin"
```

Invocation:

```bash
dutctl device1 upload-firmware ./firmware.bin
```

### Upload with Overwrite

Upload and overwrite existing files:

```yaml
devices:
  device1:
    cmds:
      upload-firmware:
        desc: "Upload firmware to device"
        modules:
          - module: file
            main: true
            options:
              operation: "upload"
              default_destination: "/tmp/firmware.bin"
              overwrite: true
              mode: "0644"
```

Invocation:

```bash
dutctl device1 upload-firmware ./firmware.bin
```

### Upload Executable Script

Upload a script with executable permissions and create directories:

```yaml
devices:
  device1:
    cmds:
      upload-script:
        desc: "Upload executable script"
        modules:
          - module: file
            main: true
            options:
              operation: "upload"
              default_destination: "/opt/scripts/test.sh"
              forcedir: true
              overwrite: true
              mode: "0755"
```

Invocation:

```bash
dutctl device1 upload-script ./scripts/test.sh
```

### Download Logs

Download log files from the device:

```yaml
devices:
  device1:
    cmds:
      fetch-logs:
        desc: "Download logs from device"
        modules:
          - module: file
            main: true
            options:
              operation: "download"
              default_destination: "dut.log"
```

Invocation:

```bash
dutctl device1 fetch-logs ./logs/dut.log
```


### Colon Syntax Behavior

**Important:** Colon syntax is **ignored** when `default_destination` is set in the config.

**When `default_destination` is set:**

```bash
# Config has default_destination: "/tmp/firmware.bin"
# The colon syntax is IGNORED, file goes to /tmp/firmware.bin
dutctl device1 upload-firmware ./firmware.bin:/custom/ignored.bin
```

**When `default_destination` is NOT set:**

```bash
# Colon syntax works as expected
dutctl device1 upload-file ./firmware.bin:/custom/path.bin

# Or for downloads
dutctl device1 fetch-logs /var/log/custom.log:./logs/custom.log
```

## See Also

- [Module Guide](../../../docs/module_guide.md) - General module documentation
