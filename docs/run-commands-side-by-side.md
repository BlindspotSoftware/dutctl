# Run your commands side by side

You can run several of your own commands on one device at once, such as a console that watches the device boot while
you switch its power from a second terminal. Two runs of the same command never run at once, and other users wait
until your last command has ended; [About device access](./device-access.md) explains why.

To watch a device boot while you switch its power, with `console` configured as
`serial -i`:

```
terminal 1$ dutctl dev console              # Ctrl-A x ends it
terminal 2$ dutctl dev power off
terminal 2$ dutctl dev power on
```

- Wait for `--- Connected to … ---` before you switch the power, or the first
  lines of the boot are lost.
- You need no reservation for it, but one keeps others out between your
  commands.
- `dutctl list` shows the device in use by you until the last of your commands
  has returned.
