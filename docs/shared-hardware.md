# Plan commands for shared hardware

A user may run several of their commands on one device at once, and dutagent does not know which hardware a command
uses. This guide shows how to configure commands so that users can run them side by side without harm.

- Run dutagent as a user of its own, never as root, as the packaged unit does:
  only then does a second open of a serial port fail. As root, two commands
  share the port's bytes.
- A user's commands can run at the same time. Commands that use one serial
  port, GPIO pin, PDU outlet, BMC or upload destination can therefore overlap;
  where an overlap would hurt, give that hardware to one command, and keep
  steps that must run in order in one command.
- Keep the console and the power control in separate commands, so users can run
  them side by side.
- A command never runs twice at once: if two people or jobs need the same
  function at once, configure it twice under two names.

[About device access](./device-access.md) describes what users may do, and what dutagent takes care of.
