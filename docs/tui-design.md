# dutctl TUI: PoC design

Status: PoC as built on branch poc/tui; section 13 lists the deviations, section 14 the changes
after the owner's first trial.

Section 14 wins over every section before it, §13 included: it records what the owner asked for
after trying the PoC and what changed for it. Sections 0 to 12 keep the design as it was decided, a
synthesis of three design proposals and two reviews; where one of them and §13 disagree, §13
describes what was built, and each of its entries names the section it changes. Sizes and the commit plan (§0, §9.1, §9.5) are updated to the build.
Facts were verified against `cmds/dutctl`, `internal/output`, `internal/style` and the module cache
(bubbletea v2.1.1, bubbles v2.2.1, lipgloss v2.0.6, cancelreader v0.2.2, x/sys v0.50.0). The
decision that needs a record is ADR-0039, `proposed`.

## 0. Summary for the owner

1. Bare `dutctl` on a terminal (stdin and stdout are TTYs, text format, `TERM` not dumb) opens an inline TUI (terminal user interface); everywhere else, and for every argument form, the client behaves exactly as today, so scripts never notice.
2. The TUI is a prompt with a breadcrumb inside it, a completion menu under it and one footer row, in Claude Code's inline balance; finished work stays in the terminal's own scrollback as `●`/`⎿` lines.
3. Three levels, `host › device › command`, each fetched from the agent when entered (`List`, `Commands`, `Details` per command); the start screen lists your recent devices and hosts from a small local history file.
4. A run hands the real terminal to the existing run path in-process (option B); the raw-console code, Ctrl-A escape, CRLF handling and formatter are reused unchanged, and one real defect, the stdin reader that stays parked after a run, is fixed at the root for the CLI too. A second shared defect found while building, an unreachable agent reported as `write envelope: EOF`, got a `fix:` commit of its own (§13, 25).
5. A raw console is announced by a violet `▌ raw console …` banner at the only moment it is known, a `▌ console closed` line on return, a `· raw console` note on the result line and a `⌨` mark on recents that opened one.
6. After a run you land on the same device's commands with the cursor on the command you just ran; only an error that invalidates the level moves you up (device gone → devices, host gone → hosts); wrong or refused arguments keep the typed line so Enter retries.
7. Enter on a command row never runs it: it puts `cmd ` into the line and shows the help; Enter on the line runs. Typed-ahead keys from a long run are flushed before the prompt returns; keys typed while a fetch is in flight are queued and replayed (§13, 29).
8. Theme: teal accent instead of Claude Code's orange, violet reserved for "the device owns the terminal", the CLI's existing gray/green/yellow/red for everything else; light and dark, ANSI-16 and `NO_COLOR` handled.
9. Size as built: ~3 100 new lines in `cmds/dutctl/tui_*.go` (estimated ~1 250) plus ~300 changed lines in the shared run path, fetchers and helpers (estimated ~150), ~2 000 lines of new tests; one new dependency family (charm v2 + cancelreader), two ADRs, a user doc page; seven commits (§9.5) that each pass CI alone.
10. Decided alone, recorded in §11: no slash commands, no terminal title, no fuzzy filter, no argument quoting, no live spinner during a run; all are follow-ups in §12.

## 1. Concept in 5 lines

1. `dutctl` on a terminal opens a **prompt**, not a full-screen app: one input line at the bottom, finished work scrolls up into the terminal's own scrollback, like Claude Code.
2. The prompt has a **context** (breadcrumb `host › device ›`); what you type is the normal `dutctl` grammar relative to that context, so `power on` at the device, or `demo-board power on` one level up, is the line a script would use.
3. **Menus** pop up under the prompt: recents and hosts, then devices with their lock state, then commands with their help as description; typing filters, Enter opens, Tab completes, Esc goes up.
4. A run is a **transcript block**: `●` header with metadata, the module's output exactly as the CLI prints it, `⎿` result with elapsed time; a console gets a banner and the whole terminal, Ctrl-A x brings you back.
5. After a run you **land where the next action is** (the same device, cursor on the command you ran); scripts never see any of this, `dutctl list` and every argument form are untouched.

## 2. Screens

All mockups are 100×30. The frame is the terminal; everything above the input box is native scrollback (printed once with `tea.Println` or by the run itself, never redrawn); the live region is the input box, the menu and side block below it, and the footer. `█` is the cursor, `›` in the menu marks the highlighted row (accent, bold), everything described as dim is the `muted` token. Rows are not padded; the right-hand columns are right-aligned to the width. The menu, help texts and lock owners in the mockups are illustrative; the real texts come from the agent.

### 2.1 Start: recent devices and hosts (bare `dutctl`, history present)

```
dutctl 1.0.0-beta.1 · TUI · jens@X1 · ~/fw/build
? shows the keys · esc goes up · ctrl+d quits

╭──────────────────────────────────────────────────────────────────────────────────────────────────╮
│ dutctl › █                                                                                       │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
  Recent devices
  › demo-board          lab:2024                          2 min ago · power on
    agent               lab:2024                          yesterday · hello Ada
    rpi-cm4             bench.local:2024                  3 days ago · console ⌨
  Hosts
    lab:2024                                              2 min ago
    bench.local:2024                                      3 days ago
    localhost:2024                                        -s default · never
  enter open · tab complete · ↑↓ move · type to filter · ? keys                     jens@X1 · ~/fw/build
```

The first two lines are printed once at start (scrollback). A recent device row shows the last line run on it; `⌨` marks a line whose last run opened a raw console. Enter on a recent device issues `List` for its host, then `Commands` for the device, and lands at the commands level (the owner's "Enter to continue"). Enter on a host issues `List` and lands at devices. A typed token that matches no row is tried as a host address on Enter (`rpc.DialAddr` completes the port).

First launch (no history file): the menu holds only the `-s` host (default `localhost:2024`). The TUI does not connect on its own; Enter does.

With `-s` given explicitly the TUI starts at 2.3 for that host; Esc leads back here.

### 2.2 Fetch in flight

```
╭──────────────────────────────────────────────────────────────────────────────────────────────────╮
│ dutctl › lab:2024█                                                                               │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
  ⠋ listing devices on lab:2024 …
  esc cancels · ctrl+d quits
```

Spinner rows appear only for unary fetches (`List`, `Commands`, lock, unlock, help); never during a run (§6).

### 2.3 Host: devices

```
● lab:2024 · 3 devices

╭──────────────────────────────────────────────────────────────────────────────────────────────────╮
│ lab:2024 › █                                                                                     │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
  › agent
    demo-board                                            locked by "bob@desk" for 25m
    rpi-cm4                                               in use by you
  enter open · tab complete · esc hosts · ctrl+r refresh · ? keys                   jens@X1 · ~/fw/build
```

The `●` line is the transcript entry for the connect, printed once per host visit (not on refresh). The annotation is the formatter's own wording (`output.LockAnnotation`, exported in the refactor): `locked by "owner" for 1h2m` for a reservation (`expires_at > 0`), `in use by "owner"` for a running command (`expires_at == 0`); the owner's own holds read `by you` in the success colour, foreign reservations in warning, foreign "in use" muted. Behind `dutserver` the list carries no lock state and the column is empty.

### 2.4 Device: commands with side info

```
╭──────────────────────────────────────────────────────────────────────────────────────────────────╮
│ lab:2024 › demo-board › █                                                                        │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
  Recent
    power on
    console ⌨
  Commands
  › console           raw console to the simulated board
    flash             flash write <image> | read <file>
    power             power on|off|toggle|cycle|status
    protect           1 module: demo-switch
    reboot            3 modules: demo-switch, demo-switch, demo-console
    unprotect         1 module: demo-switch
    update            4 modules: demo-switch, demo-flash, demo-switch, …
  Device
    lock [duration] · unlock [force] · <command> help
  ╎ Opens a raw console to the simulated board.
  ╎ Ctrl-A x ends it; `exit` on the board ends it too.
  enter pick · tab complete · esc devices · ctrl+r refresh · ? keys            jens@X1 · free · ~/fw/build
```

- **Recent**: the last 3 distinct lines run on this device (newest first), with `⌨` where the last run opened a raw console. Enter on a Recent row runs that line as it is.
- **Commands**: the `Commands` names; the description is the first non-empty line of `Details(help)`, fetched for all commands on entering the device (`tea.Batch`, one unary call each, cached per visit). Rows render at once; descriptions fill in as they arrive. A generated help `Command with N module(s): a, b` is shortened to `N modules: a, b`. Enter on a Commands row puts `name ` into the line (2.5); it never runs.
- **Device**: one reminder row for the keywords; it is not selectable.
- **`╎` block**: the next 4 non-empty lines of the highlighted command's help.
- The initial cursor is the first Commands row, never a Recent row, so an accidental Enter never runs anything.
- Footer status: `jens@X1 · free · ~/fw/build`; the middle part is the device's lock annotation from the last `List`.

### 2.5 Argument entry (the line holds a command and a space)

```
╭──────────────────────────────────────────────────────────────────────────────────────────────────╮
│ lab:2024 › demo-board › power █                                                                  │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
  › power             power on|off|toggle|cycle|status
  ╎ Switches the simulated board's power.
  ╎
  ╎ ARGUMENTS:
  ╎   on|off|toggle|cycle|status
  enter run · esc clear · ? keys                                                jens@X1 · free · ~/fw/build
```

Not a level, a view state: as soon as the first token exactly names a command (or `lock`/`unlock`) and a space follows, the menu collapses to that row and the `╎` block grows to 8 help lines. There is no argument completion: only the agent knows the values, in prose. Enter runs; a wrong argument count comes back as `InvalidArgument` and the line stays (§4).

### 2.6 A running command and its metadata

The live view is empty while the run owns the terminal (§6). The transcript, in order:

```
● demo-board power on                                                    lab:2024 · jens@X1 · 10:42:15
power: on
⎿ done · 0.3s
```

`●` line: accent marker, the line as run, a right-aligned muted tail `host · user · hh:mm:ss`. Between the markers: the module's output exactly as the CLI prints it (text formatter, column 0; `-v` adds its gray `# server=… device=…` lines; file transfers appear as the formatter's `↑ path (n bytes)` / `↓ path (n bytes)` lines). `⎿` line: the status word `done` (success colour), `failed` (error) or `interrupted` (warning), the elapsed time, and for failures the message. After the `⎿` line the prompt (2.4, cursor on `power`) is drawn again below it.

### 2.7 RAW console hand-over and return

```
● demo-board console                                                     lab:2024 · jens@X1 · 10:43:02
▌ raw console: every key goes to the device · Ctrl-A x ends it · Ctrl-A Ctrl-A sends Ctrl-A · Ctrl-A e local echo
demo-board login: root
# uname -a
Linux demo 6.1.0 #1 SMP aarch64 GNU/Linux
#
▌ console closed
⎿ done · 1m12s · raw console
```

The `▌` banner replaces the CLI's `[dutctl] raw console: …` hint inside the TUI (same facts; `▌ raw console` bold in the `console` colour, violet). It is printed by the console hook at the moment the terminal switches to raw, the only moment it is true. `▌ console closed` (muted) follows the console's existing `endRawLine` CR LF. The `⎿` line carries `· raw console` whenever the run opened one. No footer and no terminal title exist during the hand-over (the TUI is frozen, §6); the banner is the footer.

### 2.8 LINE console and an interrupted run

```
● agent prompt                                                           lab:2024 · jens@X1 · 10:45:40
▌ line console: Enter sends a line · Ctrl-D ends your input · Ctrl-C ends the run
What is your name? ^C
▌ console closed
⎿ interrupted · 4.1s
```

The LINE banner is in the accent colour; the terminal stays cooked, so Ctrl-C is SIGINT and ends the run.

### 2.9 Error states

Run refused (`FailedPrecondition`, reserved by someone else): the line is kept, the footer says so.

```
● demo-board power on                                                    lab:2024 · jens@X1 · 10:46:02
⎿ failed · 0.1s · device "demo-board" is locked by "bob@desk" for 25m
  a reservation: wait, or `unlock force` releases it (a forced unlock never ends a running command)
╭──────────────────────────────────────────────────────────────────────────────────────────────────╮
│ lab:2024 › demo-board › power on█                                                                │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
  › power             power on|off|toggle|cycle|status
  ╎ Switches the simulated board's power.
  enter retries · esc clear · ? keys                     jens@X1 · locked by "bob@desk" for 25m · ~/fw/build
```

Wrong arguments (`InvalidArgument`): the line comes back with the help beside it.

```
● agent hello                                                            lab:2024 · jens@X1 · 10:47:10
⎿ failed · 0.1s · expected 1 argument(s) but got 0
╭──────────────────────────────────────────────────────────────────────────────────────────────────╮
│ lab:2024 › agent › hello █                                                                       │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
  › hello             1 module: wrap-print
  ╎ Arguments:
  ╎   who: The name to greet
  enter run · esc clear · ? keys                                                jens@X1 · free · ~/fw/build
```

Agent unreachable (`Unavailable`, from a run or a fetch): the TUI lands at hosts with the host marked.

```
⎿ failed · 0.0s · cannot reach dutagent at lab:2024 (dial tcp: connection refused)
╭──────────────────────────────────────────────────────────────────────────────────────────────────╮
│ dutctl › █                                                                                       │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
  Recent devices
    demo-board          lab:2024                          2 min ago · power on
  Hosts
  › lab:2024                                              unreachable · enter retries
    localhost:2024                                        -s default · never
  enter open · tab complete · ↑↓ move · ? keys                                       jens@X1 · ~/fw/build
```

A fetch failure that is not `Unavailable` (for example `NotFound` for a typed device name) prints a `✗ …` line above the box and keeps the level.

### 2.10 Help overlay (`?` on an empty line; any key closes it)

```
╭──────────────────────────────────────────────────────────────────────────────────────────────────╮
│ lab:2024 › demo-board › █                                                                        │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
  keys                                       lines (type, then enter)
  enter      open the row / run the line     power on               run on this device
  tab        complete the row into the line  demo-board power on    a device first works at every level
  ↑ ↓        move the highlight              lab:2024               connect to a host (hosts level)
  esc        clear the line, else one up     lock 2h · unlock [force] · power help
  ctrl+r     refresh this level
  ctrl+c     clear the line; twice: quit     consoles
  ctrl+d     quit (empty line)               line: Enter sends a line · Ctrl-D ends input · Ctrl-C ends the run
  ctrl+a e u w k  edit the line              raw: Ctrl-A x ends it · Ctrl-A Ctrl-A sends Ctrl-A · Ctrl-A e echo
  any key closes this                                                           jens@X1 · free · ~/fw/build
```

The overlay documents Ctrl-A's two meanings (line start at the prompt, escape prefix in a raw console).

### 2.11 Degradation at 60 columns and below 40

At 60 columns (commands level):

```
╭──────────────────────────────────────────────────────────╮
│ … › demo-board › █                                       │
╰──────────────────────────────────────────────────────────╯
  Recent
    power on
  Commands
  › console
    flash
    power
    protect
    ↓ 3 more
  ╎ Opens a raw console to the simulated board.
  enter pick · tab · esc · ctrl+r · ? keys
  jens@X1 · free
```

- The breadcrumb is truncated from the left with `…`, the last segment stays whole.
- Right-hand columns (descriptions, lock annotations, recents' last line, host ages) are dropped; a device row keeps only a glyph: `●` in use, `○` locked, nothing when free, coloured as at full width.
- The `╎` block is cut to 2 lines, the menu to 6 rows with `↓ N more`; Recent to 1 row.
- The footer becomes two rows: keys, then status; the status drops the cwd first, then the lock annotation.
- The `●` header's metadata tail moves to a second muted line when it does not fit.
- Below 40 columns the box border is dropped (`› █` on a bare line); everything else degrades as above and keeps working. The TUI never exits because of the width.

Rule for all of it: `View()` builds against the width of the last `tea.WindowSizeMsg`; no rendered line is longer than the width (unit-tested at 100, 60 and 39).

## 3. Navigation model

### 3.1 Levels

| Level | Breadcrumb | Menu rows | RPCs on entry |
|---|---|---|---|
| 0 hosts | `dutctl ›` | Recent devices (host+device, age, last line, `⌨`), Hosts (age; the `-s` host always listed) | none (history file) |
| 1 devices | `host ›` | `List` entries with lock annotation | `List` |
| 2 commands | `host › device ›` | Recent (3 lines), Commands (names + first help line), Device (keyword reminder), `╎` help block | `Commands`, then `Details(help)` for every command in one `tea.Batch`; `List` for the status |

Argument entry (2.5) and the help overlay (2.10) are view states of a level, not levels. A per-level stack keeps `{cursor, filter}` so Esc restores where you were; descending resets the deeper level.

### 3.2 Keymap

Key names are bubbletea v2's (`msg.String()`): `enter`, `esc`, `tab`, `up`, `down`, `ctrl+c`, …

| Key | Menu (filter line, one token or empty) | Argument entry (line has a command + space) | Overlay / busy | During a run (terminal released) |
|---|---|---|---|---|
| `enter` | open the highlighted row: host → level 1, recent device → level 2, device → level 2, Commands row → `name ` in the line, Recent row → run that line; no match at hosts → connect to the token as an address; no match at devices → `Commands` for the token (the agent answers `NotFound`) | run the line | overlay: close; busy: nothing | LINE console: send the line |
| `tab` | put the highlighted row's name into the line plus a space; at the devices level this descends to the device (fires `Commands`) with an empty line | nothing | nothing | – |
| `up` `down` (`ctrl+p` `ctrl+n`) | move the highlight (skips section headers and the Device row) | nothing | nothing | – |
| `pgup` `pgdown` | move by a page | nothing | nothing | – |
| `esc` | line not empty: clear it; else one level up (2→1, 1→0, 0: nothing); busy: cancel the fetch and stay | clear the line (back to the menu) | overlay: close; busy: cancel | nothing of the TUI's (a byte to a console) |
| `ctrl+r` | refresh this level: 0 re-reads the history, 1 re-fetches `List`, 2 re-fetches `Commands` + help + `List` | same | nothing | – |
| `ctrl+c` | line not empty: clear it; empty: arm, footer shows `ctrl+c again quits` for 2 s; second press quits | same | overlay: close | cooked/LINE: SIGINT ends the run; RAW: byte 0x03 to the device |
| `ctrl+d` | empty line: quit at once; else delete forward (textinput default) | same | – | LINE: ends your input for this console (EOF) |
| `?` | empty line: toggle the help overlay; else a character | a character | close | – |
| `ctrl+l` | redraw (`tea.ClearScreen`) | same | same | – |
| `ctrl+a` `ctrl+e` `ctrl+k` `ctrl+u` `ctrl+w` `←` `→` | readline editing (bubbles textinput defaults) | same | – | RAW: Ctrl-A is the console's escape prefix |
| printable | filter the rows of this level, case-insensitive substring; a space after a command name enters argument entry | edit the line | overlay: close | bytes to the console |

No digit shortcuts (device and command names may start with digits), no letter bindings (letters filter), no mouse.

### 3.3 Breadcrumb and footer

The breadcrumb sits inside the input box before the prompt glyph; earlier segments muted, the current one in the accent colour. The footer has two parts in one row: key hints valid right now (left; a hand-built joined string, at most 6 items) and status (right): `user · lock annotation of the current device · cwd`. At the hosts level the status is `user · cwd`. The status row never carries key hints. An `✗` footer note (for example `no match`, `ctrl+c again quits`, `enter retries`) replaces the first key hint while it applies.

### 3.4 Type-to-filter versus a typed line

One input, resolved only at Enter; typing never fires an RPC.

1. While the line holds one token, the menu filters the rows of the current level; Enter and Tab act on the highlighted row.
2. At the commands level a space after a token that exactly names a command (or `lock`/`unlock`) enters argument entry (2.5), a pure view change.
3. Enter on a line with two or more tokens resolves it left to right from the current level with `strings.Fields` (no quoting in the PoC):
   - level 2: `cmd args…` → run; `lock [duration]`, `unlock [force]`, `cmd help` are parsed with the existing `parseLockDuration` and `parseUnlockArgs` (§7).
   - level 1: the first token is taken as a device: `Commands` is fetched with the remaining tokens as **pending**; when the list arrives the TUI lands at level 2 and, if tokens are pending, runs them as a line at once. So `demo-board power on⏎` at the devices level runs in one Enter; `demo-board⏎` lands at the commands level.
   - level 0: the first token is taken as a recent device (host from the newest history entry for it) or, if none, as a host address; the remaining tokens are pending through `List` and, for a device, through `Commands`. So `demo-board power on⏎` from the start screen connects, descends and runs with one Enter when `demo-board` is a recent device.
   - A pending line is dropped when any fetch on the way fails; the TUI lands at the deepest level reached and prints the fetch's `✗` line.
4. Tab bridges the two: `dem⇥` at devices descends to `demo-board`'s commands, `pow⇥on⏎` runs `power on`.

The TUI never validates names itself; the agent does (`NotFound` lands per §4). The one local check is the keyword grammar of `lock`/`unlock` (a bad duration is refused with a `✗` line and no RPC, as the CLI does).

### 3.5 Esc, Ctrl-C, Ctrl-D, quitting

- Esc never quits and never loses the transcript: it clears, then goes up; at the hosts level it does nothing.
- Ctrl-C: clear, then arm, then quit (Claude Code's guard, the owner's taste).
- Ctrl-D on an empty line quits at once (every shell's EOF); with text it deletes forward, so it is intercepted before bubbles' textinput sees it.
- Quit prints nothing of its own; the inline frame is erased by bubbletea, the transcript stays; the warning summary is flushed as the CLI does at exit; exit 0.
- Exit codes describe the session, not the last run: 0 on quit, 130 when SIGTERM/SIGHUP end the TUI or `kill -INT` reaches it in the menu (bubbletea's `ErrInterrupted`/`ErrProgramKilled` are mapped to `errInterrupted`), 1 when the TUI cannot start (for example the terminal cannot enter raw mode). A failed run never changes the exit code.

## 4. Landing rule after a run

Rule: **success lands where you were, cursor on what you just ran; a failure lands at the level that can fix it; the typed line is kept only when editing or retrying it is the fix.** Context is kept, nothing is re-fetched except what the failure invalidated.

| Outcome (result of `runWith`) | Lands at | Line | Cursor | Transcript / footer | Why |
|---|---|---|---|---|---|
| `nil` | 2 commands, same device; `List` refreshed silently for the status | empty | the command just run | `⎿ done · 0.3s`; `· raw console` added when one opened | the next step is almost always another command on the same device (`power on` → `console` → `power off`) |
| `errInterrupted` (Ctrl-C in LINE/cooked mode) | 2 commands | empty | same | `⎿ interrupted · 4.1s` | ended on purpose; the Recent row recalls the line |
| `errInterrupted` and the TUI context is done (SIGTERM/SIGHUP) | quit, exit 130 | – | – | `⎿ interrupted · …` | the process was told to stop |
| `InvalidArgument` | 2 commands, argument entry | the line as typed, cursor at the end | the command | `⎿ failed · … · expected 1 argument(s) but got 0`; the help block is shown | the arguments are wrong; the help is right there |
| `FailedPrecondition` (in use / locked by other / same command running / shutting down) | 2 commands; `List` refreshed for the status | the line as typed | the command | `⎿ failed · … · <agent text>` plus one hint line (table below); footer `enter retries` | the device is the thing to look at; retrying is the common next step |
| `Aborted` (module failed / panicked) | 2 commands | empty | the command | `⎿ failed · … · module failed: …` | retrying with the same args is one Enter on the Recent row |
| `NotFound`, message contains `no such command` | 2 commands, `Commands` + help re-fetched | empty | first row | `⎿ failed · … · <text>` | the config changed under you; the menu must be fresh |
| `NotFound`, message contains `no such device` | 1 devices, `List` re-fetched | empty | first row | `⎿ failed · … · <text>` | the device is gone; pick another |
| `Unavailable` | 0 hosts; the host row marked `unreachable · enter retries` | empty | that host | `⎿ failed · … · cannot reach dutagent at …` | nothing on that host can work; retry or pick another from here |
| `Unauthenticated` (lock/unlock as anonymous) | same level | empty | same | `⎿ failed · … · <text>` + `start dutctl with -u name@host` | nothing on the device changed |
| any other error (client-side file I/O, stream, `Canceled`, `Internal`) | 2 commands | empty | the command | `⎿ failed · … · <text>` | least surprise |

The `NotFound` split uses `strings.Contains` on the agent's message, the only signal on the wire. The hint line per `FailedPrecondition` text (prefix match on the agent's message, none when unmatched):

| Agent text contains | Hint |
|---|---|
| `is in use by` | `busy until that command returns; a forced unlock does not end it` |
| `is locked by` | `a reservation: wait, or \`unlock force\` releases it (a forced unlock never ends a running command)` |
| `is already running` | `the same command never runs twice at once; wait for it or run a different one` |
| `shutting down` | `the agent is stopping; try again once it is back` |

Lock, unlock and help (unary, §7) never move the level: they print their `●`/`⎿` lines and refresh the status.

Fetch failures never move the level except `Unavailable` (→ hosts, host marked); the transcript gets a `✗` line and Enter retries.

## 5. Recents and history

- **Path**: `$XDG_STATE_HOME/dutctl/history`, default `$HOME/.local/state/dutctl/history` (XDG: state holds history and logs). Directory `0700`, file `0600`. No `HOME` or an uncreatable directory → no history for the session, one `slog.Debug`, the TUI works.
- **Format**: one TSV line per event, newest last, five columns, readers ignore extra columns and skip lines with fewer than two:

  ```
  unix-seconds <TAB> host <TAB> device <TAB> line <TAB> flags
  ```

  `host` is the completed dial address (`rpc.DialAddr`, so `lab` and `lab:2024` are one host); `device` and `line` are empty where not applicable; `flags` is `raw` when the run opened a raw console, else empty. Example:

  ```
  1794470400	lab:2024			
  1794470412	lab:2024	demo-board		
  1794470420	lab:2024	demo-board	power on	
  1794470500	lab:2024	demo-board	console	raw
  ```

- **Written when**: a host line when `List` succeeded on a host visit (not on refresh); a device line when `Commands` succeeded on a device visit; a run line at `runDoneMsg` for a run the agent accepted (success, interrupted, `Aborted`, `FailedPrecondition`, other); never for `NotFound`, `InvalidArgument`, `Unavailable`, `Unauthenticated` (a typo must not become a recent). Lock, unlock and help are not recorded. Appends use `O_APPEND|O_CREATE|O_WRONLY`; when the in-memory count exceeds 1 000 lines the file is rewritten with the last 500 (temp file + rename). Two TUIs may append at once; last writer wins per line, nothing corrupts.
- **Read**: once at start and on `ctrl+r` at the hosts level. Projections, all newest-first and distinct: recent devices = distinct (host, device), 5 rows, each with the newest run line of that pair and its `raw` flag; hosts = distinct hosts plus the `-s`/default host when absent; per-device recents = distinct lines of (host, device), 3 rows, flag from the newest occurrence.
- **Privacy**: host addresses, device names and typed arguments (which may be local file names). No secrets exist in dutctl's grammar; the file is user-private like a shell history; nothing is sent to the agent. The user doc names the file; deleting it forgets everything.
- **Not a source of truth** (ADR-0001): history orders menus and prefills; every level is fetched from the agent before anything runs. Device lists, command lists and help texts are never stored.
- **Tests** set `XDG_STATE_HOME` to a temp dir (the integration harness already does).

## 6. Run integration

### 6.1 Choice: B, in-process run under a custom `tea.ExecCommand`

| | A: re-exec `dutctl` under `tea.ExecProcess` | **B: in-process `ExecCommand`** | C: capture output until `ConsoleOpen` |
|---|---|---|---|
| Change to the run path | none | small, shared with the CLI: console per run, cancellable tty stdin, send-loop wait, console hook | B plus `ReleaseTerminal()` from the receive goroutine mid-stream and a mode-switching formatter |
| `ConsoleOpen` events (banner, `· raw console`, `⌨`) | no (only the child's `[dutctl]` hint) | yes | yes |
| Connect codes for the landing rule | exit codes 0/1/130 only | `connect.CodeOf(err)` + message | same |
| Identity | per-process anonymous fallback differs unless `-u` is forwarded | one `app.user` | same |
| Live spinner during a run | no | no (the event loop blocks in `Exec`) | yes while no console is open |
| Ordering hazards | none | none: the run owns the terminal | output between `ConsoleOpen` and the release must be held back: the one racy piece |
| Rough size | ~60 LOC + the same release/restore dance | ~90 LOC shared-code change + ~200 LOC wrapper | B + ~250 LOC |

B is chosen, as all three proposals and both reviews concluded. `ExecCommand` is a 4-method interface, so no child process is needed; `Program.exec` cancels and waits for bubbletea's own stdin reader, stops the renderer with a flush, restores the pre-program termios (cooked) and sets `ignoreSignals`; `RestoreTerminal` re-enters raw mode, starts a fresh reader and re-checks the window size. A gives up the three facts the owner's asks depend on; C buys a spinner for console-less runs at the price of the only real concurrency in the design and is the natural follow-up.

### 6.2 Hand-over and take-back, one run

```
Update(enter)                                  event loop goroutine
  └─ m.running = true (View() is empty) ; return tea.Exec(&runCmd{…}, func(error) tea.Msg { return runDoneMsg{cmd} })
bubbletea Program.exec
  ├─ releaseTerminal: cancel its stdin reader, wait for its read loop, stop the renderer (erases the
  │                   empty frame), restore termios (cooked), ignoreSignals = 1
  ├─ runCmd.SetStdin/SetStdout/SetStderr: accepted and ignored (bubbletea hands over the same os.Stdin/os.Stdout the console owns)
  ├─ runCmd.Run():
  │    r.diag.release(true)                                  // diagnostics now go to the released terminal (§6.6)
  │    ctx, stop := r.runCtx()                               // signal.NotifyContext(tuiCtx, os.Interrupt); defer stop()
  │    con := r.app.console.forRun(); con.onConsole = r.announce   // fresh console state, shared streams and crlf writers
  │    r.header(con.outText)                                 // "● demo-board power on   lab:2024 · jens@X1 · 10:42:15"
  │    r.started = time.Now()
  │    r.err = asInterrupt(ctx, r.app.runWith(ctx, con, r.agent, r.device, r.command, r.args))
  │    r.app.logHandler.Flush()                              // warning summary trails the run, as in the CLI
  │    r.result(con.errText)                                 // "⎿ done · 0.3s" / "⎿ failed · … · msg" + hint / "⎿ interrupted · …"
  │    discardInput(con.fd)                                  // typed-ahead keys never reach the prompt (§6.5)
  │    r.diag.release(false)
  │    return nil                                            // the outcome travels in r, never as an Exec error
  ├─ RestoreTerminal: raw again, new stdin reader, renderer on, checkResize
  └─ go p.Send(runDoneMsg)
Update(runDoneMsg)
  └─ m.running = false; record history; apply the landing rule (§4)
```

Header and result are written from inside `Run()` through the console's text writers after bubbletea released the terminal: no ordering dependency between `tea.Println` and `Exec`, and the lines are on screen before the renderer restarts. `p.input` is `os.Stdin` itself when stdin is a terminal, so `ttyFd` and `term.MakeRaw` work on the real fd as today.

`console.forRun()` is a constructor, not a reset: it returns a new `console` with the immutable fields copied (`stdin`, `stdout`, `stderr`, `term`, `outText`, `errText`, `outTTY`, `errTTY`, `textFormat`, `tty`, `fd`) and a fresh `changed` channel; every sticky field (`open`, `id`, `raw`, `eof`, `finished`, `violated`, `warned`) is the zero value. The `crlfWriter`s are shared on purpose: the formatter and the slog handler write through them, so client text stays at column 0 while raw, inside the TUI as in the CLI.

### 6.3 The parked-stdin hazard, fixed in shared code

Today `sendLoop` blocks in `console.stdin.Read` and stays parked after a run that ended otherwise; in a long-lived process it steals the next keystrokes (and a second run on the same console ignores every `ConsoleOpen`). The fix lives in `runWith` and applies to the CLI as well (the goroutine simply ends):

```go
func (app *application) runWith(ctx context.Context, con *console, ac agentClient, device, command string, args []string) error {
    runCtx, cancelRunCtx := context.WithCancel(ctx)
    defer cancelRunCtx()
    defer con.finish()                       // registered first, runs last: raw mode ends with the run on every path

    stdin, cr := con.stdin, cancelreader.CancelReader(nil)
    if reader, err := cancelreader.NewReader(con.stdin); err == nil {   // epoll+pipe on Linux, kqueue on macOS
        stdin, cr = reader, reader           // a non-pollable stdin (a regular file) keeps the plain reader
    }

    r := &run{app: app, console: con, stdin: stdin, sendDone: make(chan struct{}), …}
    …send the command, go r.receiveLoop(runCtx), go r.sendLoop(runCtx)…

    var result error
    select {
    case <-runCtx.Done():
        if ctx.Err() != nil { result = errInterrupted }
    case err := <-r.errCh:
        result = err
    }

    // Teardown order matters: cancel first, so a send routine waiting in
    // console.await leaves; then wake a Read parked on the terminal and wait
    // for the send routine only (the receive routine ends with the stream).
    cancelRunCtx()
    if cr != nil && cr.Cancel() {
        <-r.sendDone
    }
    if cr != nil {
        _ = cr.Close()                       // releases epoll and pipe; never closes stdin
    }

    return result
}
```

- `sendLoop` reads `r.stdin`, closes `r.sendDone` on return (`defer close`), and already turns a non-EOF read error (`cancelreader.ErrCanceled`) into `r.errCh <- …` (capacity 2, never blocks) and returns.
- `cancelreader.Cancel()` returns `true` on every tty whether or not a `Read` is pending, so the wait always happens on a terminal; with a `strings.Reader` or `io.Pipe` stdin (unit tests) the fallback reader's `Cancel()` returns `false` and nothing waits: today's behaviour.
- Never wait on the receive loop: on the path where `sendLoop` reported the error, `receiveLoop` is parked in `stream.Receive()` until the agent ends the stream.
- `runRPC(ctx, device, command, args)` becomes `return app.runWith(ctx, app.console, app.agent(), device, command, args)`; the rpc.go comment about the parked routine is replaced by: "on a terminal or pipe the read is cancelled when the run ends; a regular file is read to its end as before".

### 6.4 Signals

| State | Terminal | Ctrl-C | SIGINT/SIGTERM from outside | SIGHUP | SIGWINCH |
|---|---|---|---|---|---|
| menu | bubbletea raw | key `ctrl+c` (§3.5) | bubbletea: `InterruptMsg` → `ErrInterrupted` (exit 130); SIGTERM → the TUI context (`tea.WithContext`) ends the program → `ErrProgramKilled` (exit 130); terminal restored by bubbletea | TUI context → exit 130 | `WindowSizeMsg` |
| run, no console or LINE console | cooked (released) | the tty sends SIGINT to the process group; bubbletea ignores it (`ignoreSignals`), the run's `NotifyContext` cancels → `errInterrupted` → `⎿ interrupted`; the TUI survives | same; SIGTERM/SIGHUP also cancel the TUI context → quit after the `⎿` line | same | queued; `RestoreTerminal` runs `checkResize` |
| run, RAW console | raw (console, ISIG off) | byte 0x03 to the device; Ctrl-A x ends the console (3 s grace, then cancel) | run context → `finish()` restores the terminal | same | same |

The TUI runs under its own `signal.NotifyContext(context.Background(), SIGTERM, SIGHUP)`; `dispatch()`'s SIGINT context is bypassed for the TUI branch, otherwise a Ctrl-C that ends a LINE console would mark the whole session interrupted for good. The run's context is `signal.NotifyContext(tuiCtx, os.Interrupt)`, created per run inside `Run()` through a closure (`runCtx func() (context.Context, context.CancelFunc)`), so no struct holds a context.

### 6.5 Typed-ahead input

Keys typed during a run while no console is open sit in the tty's input queue; the CLI forwards them to the console that opens next, kept. After the run, before `Run()` returns and before bubbletea starts its new reader, `discardInput(fd)` drops what is still queued, so an Enter typed during a slow `flash write` cannot trigger the landing's highlighted row. Two build-tagged files:

```go
//go:build linux
func discardInput(fd int) { _ = unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH) }

//go:build darwin
const flushRead = 1 // FREAD from <sys/fcntl.h>: the input queue
func discardInput(fd int) { _ = unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, flushRead) }
```

(`unix.TCFLSH` exists in the linux arch files, `unix.TIOCFLUSH` in the darwin ones; x/sys exports no `FREAD`.) macOS is verified manually (§10.3). Claude Code queues typed-ahead text; our prompt is a navigation surface, so dropping is the safe choice.

### 6.6 Diagnostics while the TUI is live, and the one rule

`Program.Send` and `Program.Println` block on an unbuffered channel; while the event loop sits in `Exec`, nothing may go through them from the exec goroutine (deadlock) and anything from another goroutine stalls until the run ends. So:

- **Rule (recorded in the ADR)**: no code of the TUI calls `Program.Send`/`Println` while a run holds the terminal. The run's `Flush()` and every line it prints happen under the released terminal through the console's writers.
- The slog handler writes through a `switchWriter` (~30 LOC): *live* → split into lines and `p.Println` them (fetch goroutines may log while the loop runs; a late line waits for the loop, it never deadlocks); *released* (set by `Run()` on entry, reset on exit, under a mutex) → `app.console.errText`, the shared `crlfWriter`, so a `-log debug` line printed while a console is raw gets its CR LF and no staircase. `runTUI` installs a new `cliHandler` over the `switchWriter` with the same mode and colour flag before `p.Run()`; the version advisor resolves its logger per call, so its warning lands in the new handler.
- Warn mode buffers as today and is flushed inside `Run()` after each run and at quit, so the summary trails the run as the CLI's "trailing note" (ADR-0019). `-log debug` lines appear above the live view between runs instead of over it.
- A panic in `receiveLoop`/`sendLoop` is not recovered by bubbletea (only the event-loop goroutine and `Exec` are) and can leave the terminal raw, exactly as the CLI today; a `recover` is a follow-up.

### 6.7 Metadata before, during, after

- Before: the `●` line (line as run; `host · user · hh:mm:ss` right-aligned to the width of the last `WindowSizeMsg`, passed into `runCmd`).
- During: nothing live (the TUI is frozen). The module's output, the formatter's `↑/↓` lines and `-v` metadata are what the CLI prints; the console hook prints the `▌` banners.
- After: `⎿ status · elapsed [· raw console] [· message]`, then a hint line for refusals. The footer status shows the device's refreshed lock state.

### 6.8 RAW console attention

A hook on `console`, nil in the CLI: `onConsole func(open, raw bool)`, called in `opened` where `rawHint` is printed today (after the lock is released; `rawHint` is printed only when the hook is nil) and in `closed`/`finish` after `endRawLine` when a console was open. The TUI's hook:

1. open, raw: `▌ raw console: every key goes to the device · Ctrl-A x ends it · Ctrl-A Ctrl-A sends Ctrl-A · Ctrl-A e local echo` through `con.errText` (CR LF while raw), `▌ raw console` bold in the console colour; records `r.raw = true`.
2. open, line: `▌ line console: Enter sends a line · Ctrl-D ends your input · Ctrl-C ends the run` in the accent colour.
3. close: `▌ console closed`, muted.

The escape key stays Ctrl-A, the quit grace stays 3 s, no second escape is added (ADR-0035). Lines written while the terminal is released are coloured with `style.Colorize` and ANSI-16 codes (bubbletea's downsampling is off); the console colour is ANSI 35 (`style.Magenta`, one line added to `internal/style`). Before a run nobody can know whether a console will open; the `⌨` mark on recents is the only pre-run hint possible without a proto change.

### 6.9 File transfers and the working directory

Paths named by the agent are resolved against the process working directory, as today (`os.ReadFile`/`os.WriteFile`). The TUI never changes directory and shows the abbreviated cwd in the footer status at every level, so `upload README.md` has an obvious base; the formatter's `↑/↓` lines name the paths. No file picker, no `/cd` (follow-ups).

### 6.10 Every way the terminal could be left broken, and the guard

| Hazard | Guard |
|---|---|
| run ends while raw (module returns, stream error, agent dies, Ctrl-A x) | `defer con.finish()` in `runWith` restores termios and prints `\r\n` (existing) |
| run interrupted by a signal while raw | run ctx cancels → `runWith` returns through the same defer |
| panic in `Update`/`View`/`Run()` (event-loop goroutine) or a `tea.Cmd` goroutine | bubbletea's `recover` → `shutdown` → pre-program termios restored, stack printed; `finish()` runs first on the exec path |
| panic in `receiveLoop`/`sendLoop` | not covered; the process dies with the terminal as it is, as in the CLI; `reset` fixes it (follow-up) |
| two readers on stdin | bubbletea cancels and waits for its reader before `Run()`; the run cancels and waits for `sendLoop` before returning; bubbletea's new reader starts after |
| kitty keyboard protocol, modifyOtherKeys, bracketed paste left on for a raw console | bubbletea's renderer `close()` pops/resets all three before the run; a paste into a raw console carries no `ESC[200~` |
| CRLF conversion left on after a run | `closeCurrent` disables both `crlfWriter`s (existing) |
| typed-ahead Enter re-runs a command | `discardInput` before the prompt returns (§6.5) |
| quit with a run's warnings unflushed | `Flush()` after each run and in `exit()` |
| alt screen or terminal title left changed | never used, never set |
| bubbletea's 500 ms read-loop wait expires | bubbletea proceeds; not observed with the epoll reader; manual check M6 |
| SIGKILL / OOM | nothing restores (ADR-0035 accepted consequence); `reset` / `stty sane` |

## 7. Lock, unlock and busy devices

- **Device list** (2.3): the lock annotation per row in the formatter's words via `output.LockAnnotation`; own holds `by you` (success), foreign reservations (warning), foreign "in use" (muted). The footer status at the commands level repeats the current device's annotation (`free` when none), refreshed by `List` on entering the device, after every run, lock and unlock, and on `ctrl+r`; never on a timer (follow-up).
- **Grammar**: typed as in the CLI at the commands level: `lock [duration]`, `unlock`, `unlock force`, `<command> help`. The Device row of the menu reminds of it. No slash commands, no dialogs, no new keywords (ADR-0010).
- **Execution**: unary `tea.Cmd`s over the data-returning fetchers (`lockDevice`, `unlockDevice`, `commandDetails`), no terminal hand-over for a millisecond call and no flicker. The transcript gets `● demo-board lock 2h` then `⎿ ✓ locked by jens@X1 for 2h` (the formatter's wording, rendered by the TUI through `tea.Println`), `⎿ ✓ unlocked`, or `⎿ ✗ <agent text>`; `power help` prints the full `Details` text as it is. The agent's long-duration warning (> 8 h) arrives through slog and trails the next flush as in the CLI.
- **Refusals**: a run refused with `FailedPrecondition` is a `⎿ failed` with the hint table of §4. `PermissionDenied` on `unlock` (not the owner) shows the agent's text and the hint `unlock force releases another user's reservation`. `unlock force` against a running command shows the agent's text and `the device stays busy until the command returns`.
- **Never pre-empted**: a device another user holds is still enterable and runnable; the agent decides (ADR-0024, ADR-0036); the annotation and the hint tell the user beforehand and afterwards.
- **Anonymous identity** (no `$USER`/hostname, no `-u`): lock/unlock answer `Unauthenticated`; the hint says `start dutctl with -u name@host`. The footer shows the identity at all times, so `unknown-1a2b3c4d` is seen early.
- **Relay host** (`dutserver`): `List` carries no lock state (column empty, status `-`), Lock/Unlock answer `Unimplemented`, shown as the agent's text.

## 8. Theme

Accent is teal, not Claude Code's terracotta; violet is added for one state Claude Code does not have, "the device owns the terminal"; everything else is the client's existing semantic palette (gray context, cyan transfers, yellow warning, red error, green success), so run output printed while the terminal is released (ANSI-16 via `style.Colorize`) and the live chrome (lipgloss, downsampled by bubbletea to the terminal's profile) read as one system.

| Token | Dark bg | Light bg | ANSI-16 | Used for |
|---|---|---|---|---|
| `accent` | `#2DD4BF` | `#0F766E` | 36 cyan | prompt glyph `›`, current breadcrumb segment, highlighted row marker + bold, spinner, `●` run marker, LINE console banner, `✓` |
| `text` | terminal default | terminal default | default | names, output, everything not listed |
| `muted` | `#8B949E` | `#6E7781` | 90 bright black (= `style.Gray`) | hints, footer, earlier breadcrumb segments, descriptions, right-hand columns, `⎿` prefix, `▌ console closed`, metadata tail, section headers |
| `subtle` | `#3B4048` | `#D0D7DE` | 90 | input box border, `╎` rule |
| `success` | `#3FB950` | `#1A7F37` | 32 green | `done`, own lock state (`by you`) |
| `warning` | `#D29922` | `#9A6700` | 33 yellow | `interrupted`, `⚠`, foreign reservation / in use, `ctrl+c again quits` |
| `error` | `#F85149` | `#CF222E` | 31 red | `✗`, `failed`, `unreachable` |
| `console` | `#C084FC` | `#7C3AED` | 35 magenta (`style.Magenta`, new) | `▌ raw console` banner, `⌨` mark |
| `transfer` | formatter's own | | 36 cyan (`style.Cyan`) | `↑ ↓` file lines (unchanged) |

- No background fills, no boxes except the input box: works on any background without guessing; the highlighted row is `›` + bold + accent.
- Light/dark: `tea.RequestBackgroundColor` in `Init`, `tea.BackgroundColorMsg.IsDark()` rebuilds the styles via `lipgloss.LightDark(isDark)`; dark until the answer arrives (lipgloss's default). Styles are built by a function, never package-level variables (`gochecknoglobals`).
- `-no-color`: `tea.WithColorProfile(colorprofile.Ascii)`; `NO_COLOR`: honoured by colorprofile for the chrome, and the TUI sets `app.noColor` and rebuilds `app.formatter` with `NoColor: true` so run output and released lines agree (TUI only; the CLI's `NO_COLOR` handling is a follow-up). Without colour the glyphs carry the state: `› ● ⎿ ▌ ✓ ✗ ⚠ ↑ ↓ ⌨ ○`; bold is kept for the highlighted row and the raw banner.
- ANSI-16 terminals (`TERM=linux`, old tmux): the hex values are downsampled by bubbletea's colorprofile; the fallback column is what the mapping produces and what the released lines use, so the two agree.
- Markers: `●` run header, `⎿` result, `▌` console banner ("a bar at the margin: a mode"), `⌨` raw-console recent, the formatter's `✓ ⚠ ✗ ↑ ↓ #` unchanged.

## 9. Code layout

### 9.1 Files

Everything in package `main`, `cmds/dutctl`: the TUI needs the unexported `console`, `runWith`, `asInterrupt`, `userFacingError`, the formatter and the log handler; a package boundary is a refactor the PoC does not need (follow-up), and it avoids the `ireturn`/`forbidigo` findings a split would raise.

LOC as built, the estimate in brackets.

| File | Content | LOC |
|---|---|---|
| `tui.go` | `runTUI()` entry (signal ctx, `NO_COLOR`, handler swap, `tea.NewProgram` with `WithContext`, `WithInput(app.console.stdin)`, `WithOutput(app.console.stdout)`, exit-code mapping); `model`, `level`, message types, `Init`/`Update`/`View`; fetch `tea.Cmd` builders; landing table | 1 259 (~380) |
| `tui_menu.go` | `menu`: sections and rows (kind, name, description, annotation, flags), cursor, substring filter, paging, per-level stack, `view(width)`; no bubbles/list | 215 (~170) |
| `tui_view.go` | breadcrumb + input box, `╎` help block, footer (keys + status), help overlay, width degradation, theme tokens and style builder | 699 (~260) |
| `tui_line.go` | the Enter resolver per level, pending tokens, keyword parsing via `parseLockDuration`/`parseUnlockArgs` | 219 (~90) |
| `tui_run.go` | `runCmd` (implements `tea.ExecCommand`), header/result/hint rendering, console hook, `runOutcome` classification by connect code, `switchWriter` | 373 (~200) |
| `tui_history.go` | `historyPath()`, `readHistory`, `appendHistory` (+ trim), projections | 280 (~130) |
| `tui_flush_linux.go`, `tui_flush_darwin.go`, `tui_flush_other.go` (§13, 24) | `discardInput(fd)` | 15 + 19 + 11 (2 × ~12) |
| **new total** | | **3 090 (~1 250)** |

Tests (§10): `tui_test.go` 763 (~300), `tui_menu_test.go` 240 (~80), `tui_line_test.go` 174 (~60), `tui_history_test.go` 181 (~90), `tui_linux_test.go` (pty + vt smoke) 354 (~180), `rpc_run_linux_test.go` 256 (~120), additions to `dutctl_test.go`, `rpc_test.go` and `rpc_console_test.go` ~380 (~60), `test/integration/tui_test.go` 110.

Docs: `docs/dutctl-tui.md` (levels, keys, landing, history file, limits; 138 lines), one paragraph in `README.md` "Getting Started" and in `docs/README.md`'s client section, the usage text in `dutctl.go`, ADR-0039.

### 9.2 Main types

```go
type agentClient struct {                       // one dutagent endpoint; a value, captured by Cmd closures
    addr, user string
    client     dutctlv1connect.DeviceServiceClient
}
func newAgentClient(addr, user string) agentClient   // rpc.NewDeviceClient + version advisor (from setupRPCClient)
func (app *application) agent() agentClient          // the CLI's one client

// data-returning fetchers (the xRPC functions keep their names and print the result)
func (ac agentClient) listDevices(ctx) ([]output.DeviceEntry, error)
func (ac agentClient) deviceCommands(ctx, device string) ([]string, error)
func (ac agentClient) commandDetails(ctx, device, command, keyword string) (string, error)
func (ac agentClient) lockDevice(ctx, device string, d time.Duration) (output.DeviceEntry, error)
func (ac agentClient) unlockDevice(ctx, device string, force bool) error

type level int // levelHosts, levelDevices, levelCommands

type model struct {
    app      *application
    ac       agentClient                  // current host's client; zero at levelHosts
    runCtx   func() (context.Context, context.CancelFunc)   // signal.NotifyContext(tuiCtx, os.Interrupt)
    tuiDone  <-chan struct{}              // tuiCtx.Done(): quit after a run when closed
    diag     *switchWriter
    styles   styles; dark bool; width, height int

    level    level; host, device string
    stack    [3]menuState                 // cursor + filter per level
    devices  []output.DeviceEntry
    commands []string
    help     map[string]string            // command → Details text, per device visit
    hist     history

    input    textinput.Model
    menu     menu
    busy     *fetch                       // kind, label, cancel func, seq
    running  bool
    overlay  bool                         // help
    quitArm  time.Time
    note     string                       // footer note (no match, enter retries, …)
    pending  []string                     // tokens still to resolve after a descent
}

// messages
type devicesMsg  struct{ seq int; host string; entries []output.DeviceEntry; announce bool; err error }
type commandsMsg struct{ seq int; device string; names []string; err error }
type detailsMsg  struct{ device, command, text string; err error }
type lockMsg     struct{ seq int; line string; entry output.DeviceEntry; unlocked bool; text string; err error }
type runDoneMsg  struct{ cmd *runCmd }

type runCmd struct {                         // tea.ExecCommand; Set* are no-ops
    app      *application
    ac       agentClient
    device, command string; args []string
    line     string; width int; noColor bool
    runCtx   func() (context.Context, context.CancelFunc)
    diag     *switchWriter
    started  time.Time; elapsed time.Duration
    raw      bool                            // the console hook saw a raw console
    err      error
}
```

`Update` is one switch per message type; key handling lives in `handleKey(level, state)` helpers so `funlen`/`cyclop` stay under the limits without `//nolint`. A fetch result whose `seq` is not the current one (cancelled by Esc) is dropped; a `detailsMsg` for a device that is no longer current is dropped. `View()` returns `tea.View{Content: …}` with `AltScreen` false; while `running` the content is empty.

### 9.3 The gate

```go
// dispatch(), before the signal context:
func (app *application) dispatch() error {
    if len(app.args) == 0 && app.tui != nil && app.opensTUI() {
        return app.tui()
    }
    ctx, stop := signal.NotifyContext(…)
    …
}

func (app *application) opensTUI() bool {
    t := os.Getenv("TERM")
    return app.console.tty && app.console.outTTY && output.IsText(app.outputFormat) && t != "" && t != "dumb"
}
```

`application.tui func() error` is set in `newApp` to `app.runTUI`; tests that build `&application{}` literals leave it nil and keep `list`. `route()` is untouched. The usage text's SYNOPSIS gains `dutctl` (the TUI, on a terminal) and the last paragraph reads: "When dutctl is run without any positional arguments on a terminal, it opens the TUI; elsewhere, or with a structured output format, it defaults to the list command." `-s` given explicitly is detected with `fs.Visit` and starts the TUI at that host's devices.

### 9.4 Refactors of existing code (mechanical, each with a reason)

| File | Change | LOC |
|---|---|---|
| `rpc.go` | `agentClient` with the five data-returning fetchers; `listRPC`… become `data, err := app.agent().x(); WriteContent` wrappers; `setupRPCClient` uses `newAgentClient` | +60 / −25 |
| `rpc.go` | `runWith(ctx, con, ac, …)` per §6.3; `run.stdin io.Reader`, `run.sendDone`; `sendLoop` reads `r.stdin`, closes `sendDone`; teardown block; `runRPC` delegates; comment updated | +35 / −10 |
| `console.go` | `forRun() *console` constructor; `onConsole func(open, raw bool)` called in `opened` (replacing `rawHint` when set) and in `closed`/`finish` when a console was open | +30 |
| `dutctl.go` | `tui` seam, `opensTUI()`, gate in `dispatch()`, usage text | +25 |
| `internal/output/text.go` | export `LockAnnotation`, `HumanDuration` (renames) | ±8 |
| `internal/style/style.go` | `Magenta = "35"` | +1 |
| `internal/test/pty/pty_linux.go` | `SetSize(t, f, cols, rows)` via `unix.IoctlSetWinsize` | +12 |
| `go.mod` | `charm.land/bubbletea/v2 v2.1.1`, `charm.land/bubbles/v2 v2.2.1` (textinput, spinner), `charm.land/lipgloss/v2 v2.0.6` (its go directive is 1.25.0; v2.1.0 would force 1.26.7), `github.com/muesli/cancelreader v0.2.2` (direct); as built also direct: `github.com/charmbracelet/colorprofile v0.4.3` (the ASCII profile for `-no-color`/`NO_COLOR`) and `github.com/charmbracelet/x/ansi v0.11.9` (widths and truncation), both already required by bubbletea; test-only `github.com/charmbracelet/x/vt v0.1.0` | – |

### 9.5 Commits as built (each commit builds, tests race-clean and lints on its own)

1. `fix: say that the agent cannot be reached when a run cannot start` (not planned, §13, 25; first, as the command line on main has the bug too).
2. `refactor: let the client's unary calls return their data` (agentClient, fetchers, exports in `internal/output`, fake client extended, tests).
3. `refactor: let one client process run commands one after another` (planned as "make the client's run path re-entrant": `runWith`, console per run, cancellable stdin, send-loop wait, stream close on teardown, console hook, `style.Magenta`, pty `SetSize`, cancelreader in go.mod, pty tests).
4. `feat: open a TUI when dutctl runs on a terminal` (`tui_*.go`, charm in go.mod, gate, usage text, README and docs paragraphs, `docs/dutctl-tui.md`, this file, unit + pty/vt tests, the integration scenario).
5. `docs: record the TUI on a terminal` (ADR-0039, only `docs/decisions/`).

### 9.6 Lint notes

Styles, key tables and the help text are built in functions or are `const` (`gochecknoglobals`); no `context.Context` in structs (`containedctx`), closures instead; `Update`/`View` stay under `funlen`/`cyclop` by delegation; `slog.Debug`/`Warn` only (the `cmds/dutctl` exception); `lll` 140; `varnamelen`: `m` for the model is fine within receivers, longer names elsewhere; `godot` on every comment; license header on every new file.

## 10. Test plan

### 10.1 Unit tests (`cmds/dutctl`, `go test -race`)

- `tui_line_test.go`: the resolver per level: `power on` at commands; `demo-board power on` at devices → `Commands` with pending `power on`; `demo-board power on` at hosts with `demo-board` recent → `List` then `Commands` then run; `lab:2024` at hosts → connect; `lock 2h`, `unlock force`, `power help`; a bad duration → local `✗`, no RPC.
- `tui_menu_test.go`: case-insensitive substring filter; cursor clamping and section skipping; paging windows; stack restore on Esc; `view(width)` golden strings at 100 and 60 (`-no-color`); no rendered line exceeds the width at 100/60/39; Ascii profile → no `ESC` in `View()`.
- `tui_history_test.go`: path from `XDG_STATE_HOME`, `HOME`, neither; append + trim at 1 001 → 500; projections distinct and newest-first; malformed and short lines skipped; `raw` flag; file mode `0600`; missing `HOME` → empty history, no error.
- `tui_test.go` (model driven by `Update`/`View`, fetchers faked as func fields): start → Enter host → devices with annotations; filter `demo` → one row; Enter → commands, names before descriptions, descriptions fill in from `detailsMsg`; Enter on a Commands row → `power ` in the line, never an `Exec`; Enter on the line → an `Exec` Cmd; Enter on a Recent row → `Exec`; landing table for `nil`, `errInterrupted`, every connect code of §4 (level, line, cursor, refresh flags, history written or not); the TUI-context-done case quits; Esc chain restores cursors; `ctrl+c` clear → arm → quit within 2 s, not after; `ctrl+d` quits on empty, deletes otherwise; `?` toggles the overlay and any key closes it; `ctrl+r` re-issues the level's Cmd; stale `seq` dropped; `Unavailable` from `List` → hosts with `unreachable`.
- `dutctl_test.go`: `opensTUI()` table (tty/non-tty × format × `TERM`); `TestDispatch` gains "no args, pipe → list" (unchanged) and, in a `_linux_test.go`, "no args, pty console, text → the `tui` seam is called, `listCalls == 0`".
- `rpc_test.go`: the five fetchers return data and errors (fake client extended with configurable `Commands`/`Details`/`Lock` responses); the print wrappers emit the same `output.Content` as before.
- `rpc_run_linux_test.go` (pty + `scriptedAgent`): a `runWith` over the pty slave whose run the agent ends without a console returns and `sendDone` is closed within the test timeout (the regression test for the parked reader); two consecutive runs on fresh `forRun` consoles over the same pty: keys written to the master reach run 2's console; termios raw during the console, cooked after `runWith` returns; the hook sees `(open, raw)` then `(close)` and `rawHint` is not printed when the hook is set; with an `io.Pipe` stdin `runWith` returns without waiting; `switchWriter` live/released under `-race`.

### 10.2 pty + vt smoke (`tui_linux_test.go`, unit layer per GEN-6)

Drives `runTUI` in-process on a pty slave (`internal/test/pty` + `SetSize(100, 30)`) against `scriptedAgent`; keys written to the master; output fed into `vt.NewEmulator(100, 30)`; assertions on `String()` with bounded waits (no sleeps); `XDG_STATE_HOME` in a temp dir.

- S1 start: a seeded history file → Recent devices and Hosts rows, plus the default host.
- S2 run: `demo-board power on⏎` from the devices level → `● demo-board power on` then `⎿ done`; the prompt shows `demo-board ›`; the Recent section lists `power on`; the history file has host, device and run lines.
- S3 raw: `console⏎` → the `▌ raw console` banner; slave termios raw; `Ctrl-A x` → `▌ console closed`, `⎿ done · … · raw console`; the prompt reacts to a typed `po` (proves the reader hand-back); `ctrl+d` → the program exits and termios equals the pre-test state.
- S4 arguments: scripted `InvalidArgument` → `⎿ failed · … expected 1 argument(s)`, the line still shows `hello `, the help block is visible.
- S5 refused: scripted `FailedPrecondition` "is in use by" → `⎿ failed`, the hint line, the line kept, footer `enter retries`.
- S6 unreachable: the agent closed → `⎿ failed · … cannot reach dutagent` and the hosts level with the host marked.
- S7 interrupt: `prompt⏎` (LINE console), 0x03 through the pty while cooked → `⎿ interrupted`, TUI alive (moves to manual if signal delivery proves flaky in CI).

### 10.3 Integration and manual

Integration (`test/integration`, `DUTCTL_INTEGRATION=1`, Linux, one amd64 job, real binaries, demo config, `startOnTerminal` + vt): one scenario `TestTUI` = S2 + S3 end to end (run, raw console, Ctrl-A x, quit), plus the control that `dutctl -s <addr> | cat` and `dutctl -s <addr> -f json` on the pty print exactly what `dutctl list` prints.

Manual, recorded in the PR: M1 light and dark backgrounds (GNOME Terminal, iTerm2/Terminal.app, kitty, tmux); M2 `NO_COLOR=1` and `-no-color`; M3 resize to 60 and 39 columns in the menu and during a raw console; M4 paste a multi-line text into a raw console (no `ESC[200~`); M5 Ctrl-Z in a LINE console, then `fg`; M6 `kill -TERM` while raw → terminal restored, `stty -a` sane after quit; M7 `dutctl | cat`, `dutctl < /dev/null`, `dutctl -f json`, `TERM=dumb dutctl` all list; M8 macOS: a run ended by the module, then the next key is not lost; `discardInput` drops a typed-ahead Enter; M9 `-log debug` lines appear above the live view between runs and in order during a run; M10 a real `serial -i` console on lab hardware (banner, Ctrl-A e echo).

## 11. Decision log

Sources: [U] proposal ux-faithful, [N] proposal navigator, [R] proposal robust-minimal, [JU] UX judge, [JE] engineering judge, [L] lead (this synthesis).

1. Run integration → **B**, in-process `tea.ExecCommand` → delivers console events, connect codes and a stable identity; A loses all three, C adds the only real concurrency. [U][N][R][JU][JE]
2. Package → `cmds/dutctl/tui_*.go` in package `main`, no `internal/tui`, no `Client` interface, no adapter → ~400 LOC and two lint findings less; fetchers are faked as func fields. [N][JU][JE]
3. Fetchers → methods on an `agentClient` value (addr, user, client), captured by Cmd closures → the TUI never mutates `application` while fetch goroutines run; race-clean host switching. [L]
4. Gate → `dispatch()` before the signal context, behind an `app.tui` seam; condition stdin tty ∧ stdout tty ∧ text ∧ `TERM` ∉ {"", "dumb"} → scripts, pipes, `-f json` and dumb terminals never see the TUI; `TestDispatch` gains a case instead of being bypassed. [R][JE]
5. `-s` given explicitly → start at that host's devices, Esc reaches the start screen. [U][N][R]
6. Start screen → one menu: Recent devices then Hosts (the `-s` host always listed) → the owner's "recent DUTs first, Enter continues". [U][N][R]
7. Layout → Claude Code inline: breadcrumb inside the input box, menu under it, `╎` side block, one footer row with keys left and status right; no side panel, no divider. [U][JU]
8. Menu → hand-rolled (~170 LOC), not bubbles/list → list's chrome and `q`/`esc` keymap fight the look. [N][JE]
9. Description column → first help line of every command, all fetched on entering the device with `tea.Batch`, cached per visit; rows render before descriptions → the Claude `/`-menu look without a debounce timer; N cheap unary calls accepted (the UX judge's graft over the engineering judge's fetch-on-highlight: the column needs all N anyway). [R][JU]
10. Enter on a Commands row → puts `name ` into the line, never runs; Enter on the line runs; Enter on a Recent row runs that line → one accidental Enter cannot run `reboot`; the initial cursor skips Recent. [U][JU]
11. Typed line → resolved at Enter only, `strings.Fields`, pending tokens carried through descents → typing never fires an RPC; `demo-board power on⏎` still runs in one Enter from devices and from a recent device at the start (the engineering judge's resolve-at-Enter over the UX judge's real descent, with the power-user path kept). [N][JE]
12. Name validation → none in the TUI; the agent answers `NotFound` → one rule, no duplicated grammar. [L]
13. Landing → same device, cursor on the command just run; up only when the thing is gone, hosts only when the host is gone; line kept on `InvalidArgument` and `FailedPrecondition` with `enter retries`. [U][N][R][JU][JE]
14. History → `$XDG_STATE_HOME/dutctl/history`, TSV with a `raw` flag, `O_APPEND`, trim 1 000 → 500; host and device visits plus accepted runs; never `NotFound`/`InvalidArgument`/`Unavailable`/`Unauthenticated` → shell-history semantics, a typo never becomes a recent, a host visited without a run is remembered. [U][R][JE]
15. Recent section at the top of the command list with `⌨`, no Up-arrow history → history is visible; the only pre-run raw hint without a proto change. [R][JU]
16. Ctrl-C → clear, arm, quit; Ctrl-D on an empty line → quit at once; Esc never quits → Claude Code's guard where a habit could bite, the shell's EOF where the intent is explicit (the lead's split between [U]/[R] two-step and [N] single press). [U][R][N][L]
17. Backspace on an empty line → does not go up → a fourth Backspace after clearing a filter must not jump levels; Esc is the one "up" key. [JE][L]
18. Slash commands, `..`, quoting splitter, Up-arrow history → dropped → Esc, `ctrl+r`, `ctrl+d` and `?` cover the actions; no second grammar. [JU][JE]
19. Lock/unlock/help → unary `tea.Cmd`s with `⎿` lines, not Exec'd runs → no release/restore flicker for a millisecond call; parsing reuses `parseLockDuration`/`parseUnlockArgs` (the engineering judge's concern over the UX judge's "same door"). [R][JE]
20. Header and result lines → written from inside `Run()` through the console's writers after the release → ordering-proof, one mechanism less; `Run()` returns `nil` with the outcome in the struct. [N][JU][JE]
21. Parked reader → `cancelreader` in `runWith` (not in `newConsole`), teardown `cancelRunCtx()` → `Cancel()` → wait on `sendDone` only → `Close()` → `finish()` → the CLI's `list` creates no epoll fd; no deadlock path; pipes/files keep today's behaviour. [U][JU][JE]
22. Console per run → `forRun()` constructor sharing streams and `crlfWriter`s → no sticky field to reset; the handler and formatter keep their CR LF handling while raw. [U][N][JE]
23. Diagnostics → `switchWriter`: live → `Println`, released → the shared `errText`; rule "no `Send`/`Println` while a run holds the terminal" in the ADR → no deadlock, no staircase, `-log debug` usable. [U][JU][JE]
24. Typed-ahead → `discardInput` (tcflush) before `Run()` returns, build-tagged linux/darwin → an Enter typed during a slow run cannot trigger the landing row. [U][JE]
25. Terminal title → none in the PoC → the renderer is stopped during a run, an OSC written by hand has no restorer on SIGKILL, and `View.WindowTitle` would stay after quit; the banner is the attention. [R][JE]
26. Spinner → unary fetches only, none during a run → honest about B; the `⎿` elapsed time covers the need. [R]
27. Filtering → case-insensitive substring, no fuzzy → `po` must not match `protect`. [U][JU]
28. Theme → teal accent, violet `console` token, existing ANSI-16 semantic set, no backgrounds; `NO_COLOR` honoured by the TUI and passed to the run formatter → "slightly different from Claude Code", one system across chrome and released lines. [U][N][R]
29. Width → degrade at 60, drop the border below 40, never exit → a narrow pane must not blink and die. [R][JU][JE]
30. Dependencies → bubbletea 2.1.1, bubbles 2.2.1 (textinput, spinner), lipgloss 2.0.6, cancelreader direct; vt test-only → go directive stays 1.26.0. [N][R]
31. ADRs → 0039 TUI (TTY gate additive to 0010, client state additive to 0001, ADR-0019 wording "the TUI owns the terminal; the rule applies to command-line invocations and to run output inside the TUI", the no-Send rule, the unrecovered-panic note), its own `docs:` commit; a record for Bubble Tea was planned as 0040 and dropped by the owner (§14). [U][N]
32. File-transfer count on the `⎿` line → dropped → the formatter's `↑/↓` lines already say it. [L]
33. Transcript `●` lines for navigation → only for a host connect and for runs → entering a device is shown by the breadcrumb; less scrollback noise. [L]
34. Help overlay → any key closes it → one branch. [R]

## 12. Out of scope / follow-ups

- **desc over RPC**: `DeviceInfo.desc` and a structured `CommandInfo{name, desc, args}` reply turn the description column into real descriptions and allow argument hints without N `Details` calls; a command-level console-mode hint (`raw|line|none`) lets the TUI warn before Enter. Additive proto change, own ADR.
- **Option C**: live spinner, elapsed and `esc to interrupt` while no console is open; background runs; typed-ahead queueing.
- **Extract** the run path and the TUI into an `internal/clientui` package once the PoC settles (adds `Client` to the `ireturn` allow list after asking).
- **`recover`** in `receiveLoop`/`sendLoop` turning a panic into a run error (also for the CLI).
- **Terminal title** (`View.WindowTitle` between runs, an OSC during raw) once a restore strategy exists.
- **Honour `NO_COLOR`** in the plain CLI (own `fix:`).
- **Shell-style quoting** in the line, `@path` completion for file arguments, a file picker, `/cd`.
- **Ctrl-R** reverse search over history; `DUTCTL_NO_HISTORY`; a `/forget`; agent-side recents (ADR-0024 would allow `List` to carry more state).
- **Poll `List`** on a timer while idle (lock annotations go stale after minutes).
- **Mouse** support (off in the PoC).
- **teatest** once it is tagged (pseudo-version only today).
- **Windows**: `discardInput` and the pty tests are unix-only; the TUI would run but is untested.
- **A hosts level from `dutserver`** once a relay RPC lists agents.

### 12.1 Known gaps of the PoC as built

- A host typed in part and Enter dials what was typed (`bench` → `bench:2024`), not the known `bench.local:2024`; Tab or ↓ picks the known host (the price of §13, 30).
- With nothing highlighted, Tab completes the first row shown at the commands level, a Recent line too; Tab never runs anything.
- A paste while a fetch is in flight is dropped, not queued like typed keys (§13, 29).
- A run without a console that SIGTERM or SIGHUP ends (no `^C` echoed) leaves an empty line before `⎿ interrupted`.
- The `switchWriter` drops a burst of more than 256 diagnostic lines while the TUI draws (§13, 14).
- Two tests wait with a fixed sleep: one already on main in `rpc_console_test.go`, and one in the pty tests, which cannot see when the send routine reaches its read without a hook in the run path; a missed read weakens the test, it cannot make it fail.
- Not checked: macOS (M8), and `NO_COLOR` at runtime (unit tests only).
- ADR-0039 is `proposed`; `.github/skills/code-review/decisions.md` lists accepted records only and gets its entry when it is accepted (GEN-15).

## 13. Deviations during implementation

1. §6.3 `runRPC` passes `app.console.forRun()`, not `app.console` → with lint skipping tests, `unused` flags `forRun` without a non-test caller in the second refactor commit; a fresh console per run is also what the TUI does.
2. §6.3 the teardown also closes the stream (`CloseRequest` + `CloseResponse`, before the input is released and before `finish`) → Go's HTTP/2 client stops watching the request context once the agent answered while the request body is open, so a cancelled run never reset its stream: the receive routine and the agent's module outlived the run in a process that goes on (`TestRunWithInterruptedEndsTheStream`); and a send held by flow control, to a module that stopped reading its console, returns only once the stream is closed, so waiting for the send routine first hung a cancelled run for good (`TestRunWithCancelledWhileASendIsHeld`).
3. §6.3 the teardown is two defers (`closeStream` after the run struct, `releaseInput` after the command was sent) instead of a block after the select → the send routine is awaited only when it started (a failed command send returns before it), and the select keeps its early returns.
4. §6.3 the `cancelreader.NewReader` result is checked in its own statement, not `if …; err == nil` → `noinlineerr`.
5. §6.8 the close call is `onConsole(false, leftRaw)` (whether the closing console had the terminal raw) and also fires for the implicit close when an open replaces a console → the design left close's `raw` and that case open.
6. §7 the > 8 h lock warning lives in `lockDevice`, so the CLI and the TUI both get it → it is the client's warning, not the agent's.
7. §9.4 `style.Magenta = "\033[35m"`, not `"35"` → the package's colours are full SGR sequences that `Colorize` prepends as they are.
8. §9.4 `pty.SetSize(t, f, cols, rows uint16)` → `gosec` G115 flags an int to uint16 conversion; callers pass constants.
9. §10.1 the parked-reader test opens a LINE console that the agent ends without input, and the `io.Pipe` case is the existing `TestRunRPCEndsWithoutInputEnd` (now through `runWith`) → the read is only reached with a console open; without one the routine waits in `await`.
10. §10.1 `newConsoleApp` closes the client connections before `httptest.Server.Close` → a test failing while its scripted agent waits would otherwise hang in cleanup until the test timeout.
11. New files carry `Copyright 2025`, not the current year → CI's license check (`.licenserc.json`) matches that exact text, and `.github/copilot-instructions.md` says so; this holds for the feat commit's files too.
12. §2.1, §2.3 the greeting and every line printed above the prompt before the first frame are held back until the terminal answers the background query, or 150 ms passed → bubbletea's `insertAbove` before its first flush takes the frame for the whole screen: it writes at the top and pushes the terminal's last lines off (seen with `-s`, where `● host` arrived first).
13. §6.4 no `tea.WithContext(tuiCtx)`: SIGTERM/SIGHUP reach the model as a `signalMsg`, and a `tea.WithFilter` turns bubbletea's own SIGINT/SIGTERM answers (`InterruptMsg`, a `QuitMsg` the TUI did not ask for) into it → a killed program leaves its frame on the screen, and bubbletea's SIGTERM `QuitMsg` raced the context (exit 0 or 130).
14. §6.6 the `switchWriter`'s live mode queues lines (capacity 256, a burst beyond is dropped) for a forwarding goroutine that calls `Program.Send` → `cliHandler` writes under its mutex; a writer blocked on the event loop would deadlock the run's `Flush`, which runs on that loop.
15. §6.8 the banners, `●` and `⎿` lines go through `con.outText` (stdout), not `errText` → the TUI draws on stdout, so its transcript stays together when stderr is redirected.
16. §6.8 a console closed by an interrupt starts `▌ console closed` on a fresh line, and the `⎿` line of an interrupted run whose console did not close with it starts on one too → the cooked terminal echoed `^C` without a line end.
17. §3.1 no per-level `{cursor, filter}` stack: Esc highlights the row the prompt came from and clears the filter → the same "back where you were" with less state.
18. §2.4, §3.4 the highlight never lands on a Recent row by itself, not even by an exact filter match; with only Recent rows matching, nothing is highlighted until a move → in the E2E check `keys` + Enter matched the recent line `keys` exactly and ran it on the first Enter.
19. §2.10 the help overlay stacks its sections (keys, lines, consoles) → the two-column mockup is 118 columns wide and was cut at 100.
20. §3.3 footer: the middle key hints give way before the lock state, and the working directory is cut from the left (`…/dutctl`) before it is dropped → with a long cwd the 100-column footer lost the lock state.
21. §2.11 the right-hand columns drop below 70 columns (`narrowWidth`), not only at 60 → at 60–69 they no longer fit beside a name.
22. §8 lines printed with `tea.Println` are coloured like the released lines (ANSI-16 via `style.Colorize`) → bubbletea writes them as they are, without downsampling.
23. §9.2 the model has pointer receivers; `Update` and the signal filter carry `//nolint:ireturn` → bubbletea's interfaces return `tea.Model` and `tea.Msg`.
24. §9.1 a third build-tagged file, `tui_flush_other.go` (no-op) → `GOOS=windows` and `freebsd` builds of dutctl worked before.
25. §6.3 shared fix, own `fix:` commit, the first of the series: the run's first `Send` returning `io.EOF` is followed by `Receive`, so an unreachable agent reads `cannot reach dutagent at …` (Unavailable) instead of `write envelope: EOF` → without it the Unavailable landing (§4, S6) never fired; the CLI on main has the same bug.
26. §2.5 `lock` and `unlock` in argument entry show a row of their own with a one-line description → the menu would otherwise be empty there.
27. §10.2 the smoke tests read scrollback and screen, and S7 is skipped (the pty is not the test process's controlling terminal); `TestTUI` covers Ctrl-C in a LINE console instead.
28. §10.3 the harness gives every client terminal a 100x30 size (`startOnTerminalEnv` adds env such as `TERM`) → the TUI needs a size; the other terminal tests do not depend on it.
29. §3.2 keys typed while busy are queued and acted on in order once the answer is in (Esc, Ctrl-C and a failed call drop them) → dropping them cut a line typed ahead in the middle, and its Enter ran the tail on the device (`agent⏎echo typed ahead⏎` ran `ed ahead`).
30. §2.1, §3.2 at the hosts a filter never highlights a host row: Enter dials the token as typed, Tab completes the first host shown, a move highlights one → hosts are an open set; `192.168.1.1` matched `192.168.1.10:2024` and opened the wrong agent.
31. §3.3 the `no match` note only at the commands level → at the hosts and at a host Enter still acts on an unmatched token (dials it, asks for that device), so `no match` read as if Enter did nothing.
32. §2.11 the menu window's `↑`/`↓ N more` count only rows that can be highlighted, a window scrolled into a section shows its title in place of its first row (or starts at the next title), and it never ends on a title → titles and the keyword reminder were counted as hidden commands, and recent lines read like commands without their title.
33. §2.11 the live view is never taller than the terminal: the menu takes at most what the box, a two-row footer and the `↑`/`↓` notes leave, then the help block, the menu's end and the overlay's end give way → bubbletea cut a too-tall view from the top, the box with the line typed into first (100×12).
34. §2.10 the overlay lists `lock 2h`, `unlock [force]` and `power help` as three rows with their meaning → the one-line reminder read as the meaning of `lock 2h` once the overlay was stacked (entry 19).
35. §2.6 every `●` run header starts after an empty line → the runs read as blocks of their own in the scrollback; the mockups showed none (found while writing the ADRs).

## 14. The owner's feedback after the first trial

The owner tried the PoC and asked for seven changes, R1 to R7 in his order. Each entry names the
sections it overrides, what changed and, after the arrow, why.

- R1. An experimental notice with the synopsis at start, for a while (§2.1): before the program starts, `runTUI` writes the `SYNOPSIS:` block of `dutctl -h`, taken from the `usageSynopsis` constant in `dutctl.go`, its empty lines at both ends trimmed and its tabs expanded to tab stops, in gray, then an empty line and a two-line notice in yellow, `EXPERIMENTAL` bold also without colour, which says that dutctl can be used as usual, as shown above, and that `dutctl -h` has the details; it goes straight to the terminal through the console's text writer and stays on screen directly above the greeting. `dutctl.go` and `dutctl -h` are unchanged. It shows on every start, also with `-s`. It lives in `tui_splash.go`, with one call site, its own test and the last commit of the series, so dropping that commit removes it; the pty tests and the integration test read the terminal after it. No modal screen, and no `tea.Println`: written before the program starts, it comes first whatever the renderer does. The notice lines are at most 78 columns and wrap on a narrower terminal → the owner wants the TUI's first users to know it is a preview and that the command line works as usual.
- R2. A console's colour shows again when it ends (§2.7, §2.8, §6.8, §8): a console is bracketed by two lines in its colour with one title, `▌ raw console: …` and `▌ raw console closed` in violet, `▌ line console: …` and `▌ line console closed` in blue; the whole line is coloured and the title bold, also without colour; one helper, `consoleLine`, renders both → the gray `▌ console closed` did not show where a console's session ended.
- R3. The menu folds after a run (§2.4, §2.5, §2.9, §3.2, §4): the `╎` help block shows only while a command's arguments are typed, 8 lines, 2 on a narrow terminal, the last one `… <command> help prints all of it` when the help is longer, never while browsing; on a short terminal the block takes only the rows the menu leaves, so that line stays in sight (§13 entry 33 cut it from the end, found at 100×12). After the outcome of a line (a run done, interrupted, aborted, refused, unauthenticated or failed otherwise; lock, unlock and `<command> help`) the menu folds to one line, the strip: the highlighted row first with `›`, then the other rows muted in menu order, `  › power · console · … · lock · unlock`, cut with `…`; a refused run's kept line shows the command's row alone, without the help. Wrong arguments keep the menu open with the help, which is the fix; no such command, no such device and an unreachable agent land unfolded elsewhere. A change of the line, the first `↑ ↓ pgup pgdown` (the highlight stays), a level change or a menu reset unfolds it; Enter and Tab act as unfolded; the footer reads `enter pick · ↑↓ list · tab complete · esc devices · ? keys`. After `<command> help` the highlight is on the command, as after a run. `maxMenuRows` keeps its limit → the menu and the help block took most of the terminal after every run and pushed its output out of sight.
- R4. The line console gets its own colour (§2.8, §8): blue, `style.Blue`, bright blue `\033[94m`, as plain blue is hard to read on a dark terminal; the run header's `●` stays cyan, the accent. Consoles are blue where the terminal edits the line and violet where the device owns the terminal → the cyan line banner read like a run header.
- R5. No recent lines at a device (§2.4, §2.11, §3.1, §3.2, §5, §11 decisions 10 and 15, §13 entry 18): the `Recent` section of a device is gone, with its row kind, `recentLinesMax`, `history.deviceLines`, the recent-line clause of `menu.safe` and the `⌨` name mark. `Recent devices` at the start stays, with the last line run on each and its `⌨`, so the history still records the lines the agent accepted; the hosts stay newest first, a default or `-s` host never visited last, now pinned by a test → moving to a command is quick enough that the section only took room.
- R6. `lock` and `unlock` are rows (§2.4, §3.2, §3.4, §7, §13 entry 26): the `Device` section holds them as command rows, highlighted, filtered and completed like commands; Enter on the row or on the typed word puts `lock ` into the line and Enter on the line runs it, while `lock 2h` and Enter runs at once, as `power on` does. The reminder row, its row kind and the special case that ran a typed `lock` or `unlock` alone at once are gone; the highlight starts on the first command. The `?` overlay's lines that run on the first Enter list `unlock force` instead of `unlock [force]` → the keywords could only be typed, and `lo` + Enter and `lock` + Enter did different things.
- R7. The TUI's lines are told apart from module output (§2.6, §6.7, §8): the module's output stays as it is, and the transcript follows two rules: a line of the TUI's starts in column 0 with `●`, `⎿`, `▌` or `✗`, or is indented under one, and none of its text is in the terminal's default style. The run, lock, unlock and help headers are bold after the cyan `●`, the connect line's host bold and its count gray, a failure's message red; without colour bold and the glyphs remain. A TUI line never starts in the middle of a line: the console's text writers (`crlfWriter`) to the terminal share whether the last write left its line open, and the TUI ends that line before a console line and the result line; the echoed `^C` and a raw console's end keep their handling, without a doubled empty line: the `\r\n` that ends a raw console's line bypasses the writers and clears the flag. The run's warning summary, `⚠ N warning(s) during run:`, follows the ended line and precedes the result line; its `⚠` header stays yellow and its lines are gray, as are the debug lines of `-log debug`, for the TUI sets the log handler's `muted`; `⚠` is the one glyph besides the four, the client's warning marker. The greeting's name is bold also without colour → plain text from the TUI looked like a module's, and a module's output without a line end put `⎿ done` or the warning summary behind it.
- No decision record for the library (§9.4, §11 decisions 30 and 31): the owner decided against an ADR for Bubble Tea, so the choice stays in this document. Bubble Tea v2 with Bubbles and Lip Gloss draws inline, prints above its view into the scrollback, and its `tea.Exec` hands the terminal to a Go value after it cancelled and awaited its own input reader. tview on tcell owns the whole screen: it draws on the alternate screen and leaves nothing in the scrollback. Code of our own on `golang.org/x/term` needs no dependency, but key decoding, the inline redraw, width measurement, resizes, background detection and a cancellable reader would be dutctl's to write and to keep working on every terminal. The cost: 18 more modules, one of them, `charmbracelet/ultraviolet`, by pseudo-version only; the client binary grows from 13.6 MB to 16.0 MB; Bubble Tea v2 is young, and its event loop waits while a run holds the terminal. Lip Gloss stays at v2.0.6, as v2.1.0 declares `go 1.26.7`.

Known gaps of this round:

- A line console answered with Enter, after which the module prints nothing, leaves an empty line before `▌ line console closed`: the terminal's echo of the line end does not pass the writers.
- Whether a line is open is tracked per process, for stdout and stderr together when both are terminals; two different terminals for them are not told apart.
- Only a run flushes the log handler: the warnings of a lock, an unlock, a help or a fetch, such as the version check of the first `List`, show in the next run's summary, under `during run`, or at the exit.
- The `-log debug` lines are written as they come, also in the middle of a run's line, and start with their level, not a glyph; gray tells them apart only with colour.
- `[dutctl] local echo on` and `off`, after Ctrl-A e in a raw console, stay plain: the escape filter that writes them is the command line's too.
