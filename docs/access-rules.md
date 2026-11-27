# Device access rules

What dutagent does with a command or a reservation, by the state of the device. [About device
access](./device-access.md) explains the terms and why.

## Who can start what

| The device … | `dutctl dev cmd` |
| --- | --- |
| runs nothing | starts |
| runs other commands of yours | starts next to them |
| runs `cmd` for you, or is still stopping it after a cancel | refused |
| is reserved by someone else, or runs their command | refused |
| belongs to a dutagent that is stopping | starts only on a device you reserved |

The refusals read:

```
device "dev" is already running "flash" for "alice@lab"; a command does not run twice at once, and a cancelled one keeps running until it has stopped
device "dev" is in use by "bob@lab"
device "dev" is locked by "bob@lab" for 25m
dutagent is shutting down and takes no new work; try again once it is back
```

## When the reservation ends while commands run

| The reservation … | Your running commands | Your next command | Other users |
| --- | --- | --- | --- |
| expires, or you `unlock` | go on | as before | refused until your last command has ended; then the device is free |
| is ended by someone's `unlock force` | go on | as before | the same |
| ends because dutagent aborts its work | are aborted | refused | refused |
