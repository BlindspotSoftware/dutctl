# What ends a command or the agent

Every trigger that ends a command, a reservation or dutagent, and what follows for the device, its lock, the
modules' Deinit and the user. [About the agent's lifecycle](./dutagent-shutdown.md) explains the stages.

| Trigger | What ends | Device and lock | Deinit | What the user sees |
|---|---|---|---|---|
| **User console, client machine** | | | | |
| Ctrl-C in `dutctl` | the user's own command | busy until the module has returned: at once for most tools, up to 10 s for flash; the user's reservation stays | no | the prompt at once; retrying the same command at once is refused until it has stopped, the user's other commands may run |
| `dutctl <device> unlock` | the user's own reservation | a running command stays busy | no | – |
| `dutctl <device> unlock force` | someone else's reservation | never ends a running command | no | an error if the device is only busy |
| command refused | nothing starts | unchanged | no | `FailedPrecondition`: held, reserved, the same command already running or agent shutting down; `NotFound`, `InvalidArgument` |
| **Network** | | | | |
| connection lost | the command, once the agent notices: after about 2.5 min, up to about 15 min with data in flight | busy until then | no | a connection error |
| **Within the command, on the agent machine** | | | | |
| module done | the command | free | no | output, exit code 0 |
| module error or panic | the command; later modules do not run | free | no | `Aborted` "module failed: …", for a panic "module failed: module panicked: …" |
| stream or protocol error | the command | busy until the module has returned | no | the stream's error |
| module does not return after an abort | nothing | stays busy, the agent log warns every 10 s; `unlock force` does not help | no | the same command and other users' commands are refused until the admin ends the agent |
| reservation expires | the reservation, default 30 min, chosen by the user | free, unless a command runs | no | – |
| **Admin console, agent machine** | | | | |
| first stop signal: Ctrl-C in the terminal, `kill`, `systemctl stop` or `restart`, reboot | the agent enters stage 1 | commands and reservations go on | afterwards | new reservations, and commands on devices the user has not reserved: `FailedPrecondition` "dutagent is shutting down…"; a reservation's owner may still run commands on it and extend it |
| stop signal during stage 1: Ctrl-C, `systemctl kill --kill-whom=main` | stage 2: all running commands | busy until all modules have returned; reservations end | afterwards | `Aborted` "module execution aborted: dutagent is shutting down" |
| stop signal during stage 2, `SIGQUIT` (Ctrl-\\), `kill -9` | the agent at once; exit 1 after the stop signal | all locks gone, hardware stays as it is; without a service manager that kills them, the tools it started may keep running | no | the running command fails with a connection error, then "cannot reach dutagent" |
| **Within the agent, failures** | | | | |
| Init fails, or a stop signal during the start | the start | – | yes | "cannot reach dutagent" |
| server failure, e.g. address in use, or a panic in the main flow | the agent, through stage 2 | busy until all modules have returned | afterwards | the connection drops |
| Deinit fails or takes over 15 s | – | hardware state unclear, log "UNKNOWN STATE" | cut short | – |
| crash from an agent bug, a panic in a background goroutine | the agent at once | all locks gone | no | the connection drops |
| **Kernel and hardware** | | | | |
| OOM killer, power loss | the agent at once | all locks gone, hardware stays as it is | no | the connection drops |
