# Hive — Distributed Terminal Workspace Client

Planning document. MVP is in `cmd/hive`.

Inspected: [theo-kirby/tmax](https://github.com/theo-kirby/tmax) at `65874a7` (2026-09-06),
tmux control-mode sources (`control.c`, wiki), and Sessh’s existing attach path.
tmax is a two-day-old Python tmux plugin; the README is not a complete
description of the implementation.

Working name: **Hive**. The product is a standalone workspace client, not a
tmux plugin and not a Rust rewrite of tmax.

---

## Verdict

A direct distributed client is a real architecture, not a restyling of tmax.
The meaningful improvement is **not** “talk control mode instead of proxying.”
It is this split:

| Concern | Owner |
|---|---|
| Workspace identity (`devbox/backend`) | The client |
| Persistence, panes, copy-mode, remote prefix | Remote tmux, unchanged |
| Transport, auth, jumps | OpenSSH, unchanged |
| Discovery, attach, detach, reconnect | The client |

tmax solves a different problem well: *keep living inside local tmux, and make
remote sessions look like local ones.* That forces fake local sessions, a
per-pane Python/SSH bridge, and incomplete command translation.

Hive should not do that.

The interactive path should be the one that already works:

```
ssh -t devbox tmux attach-session -t backend
```

Control mode is a **sidecar** for metadata and commands, not the keyboard/display
path. Using `tmux -CC` as the interactive pipe would recreate tmax’s worst
costs (command-RTT input, escaped `%output`, missing copy-mode) without
keeping tmax’s best asset (local tmux as a renderer).

This is also the model Sessh already uses on device. Hive is the desktop TUI
of that model, with a global index across machines.

---

# Deliverable 1 — tmax technical analysis

## Architecture

tmax is a **local tmux plugin**. Language/runtime: **Python 3.9+** plus bash
3.2-compatible shell, loaded by `tmax.tmux`. There is no daemon, no compiled
binary, and nothing installed on remotes.

```
local tmux server
  ├── key bindings / hooks          (tmax.tmux)
  ├── fzf session switcher          (remote.py switch)
  ├── optional sidebar pane         (sidebar.sh + sidebar-ui.sh)
  └── per remote session
        ├── placeholder session     host/name, @tmax-remote-* options
        ├── watch process           topology reconcile
        └── per opened remote pane
              └── view process      Python + ssh + tmux -C
```

Entry: `tmax.tmux` binds `prefix + Space` (fzf switcher), `prefix + s`
(sidebar or native tree), wraps recognised prefix bindings, and installs a
`client-session-changed` hook that calls `remote.py activate`.

## Source-code structure

| Path | Role |
|---|---|
| `tmax.tmux` | Plugin entry. Options, bindings, hooks. Calls `remote.py install`. |
| `scripts/remote.py` | Entire remote stack + switcher. ~850 lines, one file. |
| `scripts/sidebar.sh` | Open/close/follow sidebar; local overview tiles. |
| `scripts/sidebar-ui.sh` | Interactive list inside the sidebar pane. |
| `scripts/preview.sh` | One overview tile (`capture-pane` loop). |
| `remotes.json` | User host list. Not auto-discovered. |
| `test/control_mode_test.py` | Isolated `-C` probe: `send-keys -H`, split, detach. |
| `test/remote_integration_test.py` | End-to-end SSH + isolated sockets. |
| `REMOTE-PLAN.md` | Short design note; matches the code. |

The remote implementation landed in one commit (`5049359`) and has been
polished since. Almost all behaviour that matters is in `remote.py`.

## Process model

`remote.py` is a multi-command script. Long-lived work is `Popen`’d via
`spawn()` (`start_new_session=True`, stdin closed, logs to
`/tmp/tmax-UID-HASH/remote.log`).

| Command | Lifetime | Count |
|---|---|---|
| `install` | once at plugin load | 1 |
| `switch` / `switch-refresh` | popup duration | 1 + background refresh |
| `refresh` | seconds | up to 1 per host (flock) |
| `watch` | while proxy session exists | 1 per opened remote session |
| `view` | while pane exists | **1 per opened remote pane** |
| `action` / `prompt` | milliseconds | per routed key |

`view` is the expensive one. Each local proxy pane runs:

```
python3 remote.py view <host> <sid> <remote-pane>
  └── ssh -T … tmux -C attach-session -E -f no-output,ignore-size -t <sid>
```

So a remote session with 4 panes costs 4 Python processes and 4 control-mode
clients attached to the **same** remote session, plus one `watch` process.

## SSH architecture

`ssh()` in `remote.py`:

```
ssh -T
    -o BatchMode=yes
    -o ConnectTimeout=5
    -o ServerAliveInterval=15
    -o ServerAliveCountMax=2
    [-S existing-master -o ProxyCommand=false]   # password hosts
    [-o ControlMaster=auto -o ControlPersist=120
     -o ControlPath=/tmp/tmax-…/<hash>.ssh]      # key hosts
    destination
    '<tmux> [-L socket] <args>'
```

What this actually does:

- One OpenSSH **transport** per host (ControlMaster).
- Every `fetch()` (`list-sessions`, `display-message`, …) is a new `ssh`
  process on that master.
- Every `view` opens another multiplexed channel running `tmux -C attach`.
- `-T`: no TTY on the SSH session. Control mode is a pipe.
- `BatchMode=yes`: no password prompt. Password hosts must already have a
  user-opened master (`control_path`).
- `ProxyCommand=false` on an explicit master: fail if that socket is gone,
  do not silently open a new connection.
- Runtime dir is `/tmp/tmax-UID-HASH` with mode `0700` and uid checks.
  macOS `$TMPDIR` is avoided because OpenSSH’s socket path is too long.

This is a solid OpenSSH-subprocess design. Borrow it.

## tmux control-mode usage

tmax uses **`-C`, not `-CC`**. That is correct for a pipe client. Official
wiki: `-C` leaves the TTY in canonical mode (meant for humans typing);
`-CC` disables canonical mode and emits a `\033P1000p` DSC that iTerm2
listens for. tmax’s `view` puts the *local* pane PTY in raw mode itself
(`tty.setraw(0)`), so `-C` over SSH pipes is the right choice.

Attach flags (`Control.__init__`):

```
tmux -C attach-session -E -f no-output,ignore-size -t <sid>
```

- `-E`: do not update `$TERM` / client environment from this client.
- `no-output`: start silent; `view` later unsets it and pauses every pane
  except the one this process owns.
- `ignore-size`: this client does not shrink the remote window until the
  local pane is visible, then `refresh-client -C WxH` + `!ignore-size`.

Input is **not** a PTY write. Every stdin burst becomes:

```
send-keys -H -t %pane 61 62 63 …
```

That is a control-mode command. It waits for `%begin`/`%end`. Keyboard
latency is therefore **SSH RTT + a full command round-trip**, not SSH RTT
alone. The probe in `test/control_mode_test.py` exists specifically to
prove `Ctrl-B` (`0x02`) reaches the remote application as a byte and is
not eaten as a nested prefix.

Output is `%output %pane <octal-escaped bytes>`. Python unescapes
`\\[0-7]{3}` and writes the bytes to the local pane’s stdout. Local tmux
then emulates the terminal.

Hidden panes: `refresh-client -A %pane:pause`. Pause **discards** output
without blocking the remote program (unlike `:off`, which can stop the
server reading the pane). Returning calls `:continue` and `repaint()`
(`capture-pane -p -e` plus cursor / alternate-screen / mouse / keypad
flags). Scrollback that arrived while paused is gone.

`repaint()` is an incomplete terminal-state restore: cursor, alternate
screen, scroll region, a handful of DEC private modes. The README is
honest that mixed layouts and exact terminal modes are not guaranteed.

## Local proxy architecture

Until selected, a remote session is a **placeholder**:

```
new-session -d -s host/title -n connect \
  "printf 'Select this session to connect…'; sleep 86400"
```

User options on that session:

- `@tmax-remote-host`
- `@tmax-remote-session` (remote `$id`, e.g. `$0`)
- `@tmax-remote-epoch` (`#{pid}:#{session_created}`)
- `@tmax-remote-name`, `@tmax-remote-windows`
- `@tmax-remote-status`

Selecting it (`attach` → `sync`) creates one local window per remote
window and one local pane per remote pane. Each pane’s command is
`remote.py view`. Layout strings are rewritten (`layout_translate`) so
remote pane IDs become local ones, then `select-layout` is applied.

Local names are `host/session`. Remote renames are followed. A host that
itself runs tmax has its own proxies; those are skipped via
`#{@tmax-remote-host}` so sessions are not listed twice.

`install()` rewrites prefix bindings with `if-shell -F #{@tmax-remote-host}`:

Routed: `new-window`, `split-window`, `next-layout`, `select-layout`,
`swap-pane`, `rotate-window`, `resize-pane`, confirmed `kill-pane` /
`kill-window`, window rename (`,`).

Not routed: anything typed at `:`, custom bindings, copy-mode, paste,
navigation. Those act on the **local proxy**. Killing a proxy from the
native tree only deletes the local copy; the 5–15s `watch` loop may
recreate it.

This is the “fake sessions on my laptop” model.

## Session discovery

Hosts come only from `remotes.json`. No SSH-config scrape, no network
scan.

`refresh(host)` runs `tmux list-sessions -F '#{session_id}…#{session_name}'`
over SSH, writes `/tmp/tmax-…/<hash>.json`, and keeps the previous
session list if the host is offline. Stale cache (>10s online, >30s
otherwise) triggers a background `spawn("refresh")`.

The switcher (`switch_rows`) paints **immediately** from local tmux
(placeholders + locals), then fzf’s `load` binding runs `switch-refresh`
in the background and reloads only if the text changed. That 100ms-first-
paint behaviour is worth copying.

## Reconnect behaviour

`view`’s inner loop catches `ConnectionError`, `TimeoutError`,
`RuntimeError`, `OSError`, prints
`[tmax: …; reconnecting in 5s]`, **discards stdin** for 5 seconds, then
opens a new `Control`. Before accepting the new client it checks
`#{pid}:#{session_created}` against `@tmax-remote-epoch`. Reused tmux
IDs after a remote server restart are refused.

`watch` marks `@tmax-remote-status offline` on error. If the remote
session is gone (`missing_session`), `forget_proxy` kills the local
session and moves clients to a remaining local session.

There is no workspace identity independent of the local proxy session.
Reconnect restores the pane bridge, not a client-owned “I am on
`devbox/backend`.” If the local tmux server dies, the proxies die with
it.

## Runtime trace (what actually happens)

1. **Start tmax.** `run-shell tmax.tmux` → bind keys → `remote.py install`
   wraps prefix bindings. No SSH yet.
2. **Discover a host.** Switcher or tree calls `discover()` → parallel
   `refresh` (max 4 threads) → `ssh host tmux list-sessions`.
3. **Discover sessions.** Cache file + `prepare()` creates/updates
   placeholders. Offline hosts keep last cache.
4. **Select a remote session.** `switch-client` to `host/name` →
   `activate` → `attach` → epoch fetch → `sync` → `spawn watch`.
5. **Open a window.** Routed `prefix+c` → Unix socket to the focused
   `view` process → `control.call("new-window", …)` → `watch`/`sync`
   creates a new local window running another `view`.
6. **Open a pane.** Same path with `split-window`. Another Python+SSH
   control client.
7. **Type.** Local tmux delivers bytes to the `view` PTY → `send-keys -H`
   over control mode → remote application.
8. **Output.** Remote tmux → `%output` → Python unescape → local PTY →
   local tmux emulator.
9. **Resize.** Once a second, `view` reads local window size, subtracts
   sidebar width if present, `refresh-client -C WxH`.
10. **Disconnect.** SSH dies → `Control.line` raises → banner → 5s
    discard input.
11. **Reconnect.** New `-C attach`, epoch check, pause others, `repaint`
    visible pane. Remote tmux never stopped.

## Performance characteristics (from the code, not the README)

| Resource | Cost |
|---|---|
| Processes per opened pane | 1 Python + 1 `ssh` + 1 remote `tmux -C` client |
| SSH transports | 1 per host (ControlMaster) |
| SSH channels | 1 per pane (control attach) + 1 per `fetch`/`watch` tick |
| Input | hex `send-keys` command per stdin burst; `%begin`/`%end` wait |
| Output | line-oriented `%output` with octal escaping; Python `select` 64KiB reads |
| Terminal emulation | twice: remote tmux already captured it; local tmux emulates again |
| Topology | poll `list-panes -s` every 5s attached / 15s detached |
| Flow control | manual `:pause`/`:continue`; no `pause-after` |
| High output | visible pane floods `%output` through Python; hidden panes drop data |
| Memory | scales with opened remote panes (stated in REMOTE-PLAN.md) |
| CPU | Python decode loop + local tmux redraw + remote tmux |

`CONTROL_MAXIMUM_AGE` in tmux `control.c` is **300000 ms**. A control
client that falls 5 minutes behind without `pause-after` is disconnected
with `too far behind`. tmax avoids that on hidden panes by pausing them
immediately. A visible `docker logs -f` still has to be copied, escaped,
parsed, and re-emitted.

## Strengths

- Daily-driver UX if you already live in local tmux: one prefix, one
  status bar, one tree, one fzf list.
- Nothing installed remotely. OpenSSH + tmux only.
- ControlMaster + optional shared master for password hosts.
- Epoch guard against reused pane/session IDs.
- Immediate switcher paint + background refresh. Does not block typing.
- Pause-while-hidden is the right idea for unused panes.
- Honest about limits (scrollback, `:`, custom binds).
- Integration tests actually attach over SSH.

## Weaknesses

- Global model is **local tmux**. Remotes exist only as proxies.
- Per-pane control-mode attach is far heavier than one normal client.
- Input pays a command RTT. This is the structural latency tax.
- `%output` is a worse pipe than a PTY for `htop` / `tail -f`.
- Copy-mode, choose-tree, and other tmux-drawn UI are **not** sent to
  control clients (official wiki). tmax therefore implements copy-mode
  locally, on a pane whose history started when the proxy opened.
- Binding translation will always be incomplete.
- `watch` is a poll, not `%sessions-changed` / `%window-add`.
- Placeholder `sleep 86400` sessions pollute the local server.
- Sidebar overview is local-only. Remote sessions cannot use it.
- Python `select` loop, no `pause-after`, 1s size poll.

## Borrow

- Immediate cached UI, background host refresh.
- OpenSSH ControlMaster / ControlPersist / BatchMode / ServerAlive.
- Epoch = `pid:session_created` as a generation token.
- Pause unused output; do not block remote programs.
- Skip a host’s own proxy sessions if it also runs a similar client.
- Isolated-socket tests.
- `ssh -G` / explicit destinations; do not invent a host registry.

## Avoid

- Local sessions as the representation of remote sessions.
- One control-mode client per pane.
- `send-keys -H` as the interactive input path.
- `%output` as the interactive output path.
- Rewriting prefix bindings to “make remote feel local.”
- Treating `-CC` as inherently better than a normal attach.
- Rewriting this in Rust and calling that the product.

---

# Deliverable 2 — proposed architecture

## What is actually different

Remote tmux sessions are **first-class objects in the client**, addressed
as `devbox/backend` (URI form `tmux://devbox/backend`). The SSH
connection is a handle, not the identity. Local tmux is optional and
never required as a global source of truth.

There are no local proxy sessions.

```
                    Hive client
                         │
          ┌──────────────┼──────────────┐
          │              │              │
     OpenSSH mux    OpenSSH mux    OpenSSH mux
          │              │              │
        laptop         devbox         gpu01
          │              │              │
        tmux            tmux            tmux
          │              │              │
       sessions       sessions       sessions
```

Two channels per **attached** workspace, both over the same SSH
transport:

1. **Interactive PTY** — `ssh -t host tmux attach-session -t session`.
   Remote tmux draws status, panes, copy-mode, layouts. This is a normal
   tmux client. Keyboard latency equals today’s `ssh && tmux attach`.
2. **Control sidecar** (optional, lazy) — `ssh -T host tmux -C
   attach-session -f no-output,ignore-size,pause-after=N -t session`,
   used only for `list-*`, events, and commands that must not be typed
   into the PTY. Sessh already learned this lesson (`src/tmux/ctl.ts`).

Discovery does **not** need a long-lived control client:

```
ssh -o BatchMode=yes -o ConnectTimeout=5 host \
  tmux list-sessions -F '#{session_name}\t#{session_windows}\t…'
```

## Component diagram

```
┌─────────────────────────────────────────────────────────────┐
│ Hive TUI                                                    │
│  picker │ attached PTY passthrough │ reconnect chrome       │
└────────────┬─────────────────────────────┬──────────────────┘
             │                             │
      WorkspaceIndex                AttachSupervisor
      (cache, fuzzy)                (PTY + backoff)
             │                             │
             └────────────┬────────────────┘
                          │
                   ConnManager
                   (per-host OpenSSH)
                          │
            ┌─────────────┼─────────────┐
            │             │             │
         mux exec     mux exec      PTY session
      list-sessions   tmux -C       tmux attach
      (discovery)     (sidecar)     (interactive)
```

## Data flow

**Picker**

1. Load last snapshot of `host → sessions` from disk.
2. Render immediately.
3. For each configured host, lazily open/reuse ControlMaster and run
   `list-sessions`.
4. Merge; keep last-known list if a host is offline.
5. User selects `devbox/backend` or types a new name.

**Attach**

1. Ensure host transport (ControlMaster).
2. `has-session -t =backend` (exact name).
3. Spawn `ssh -t … tmux -u attach-session -t =backend` with the
   client’s window size.
4. Raw-mode passthrough: stdin → SSH PTY, SSH PTY → stdout.
5. Intercept only the **client prefix** (not the remote tmux prefix).
6. On SIGWINCH, `TIOCSWINSZ` the SSH PTY (OpenSSH already forwards).

**Disconnect / reconnect**

1. EOF / SSH death → leave PTY, keep workspace pointer.
2. Show reconnect chrome: `devbox/backend · retry in 1s`.
3. Exponential backoff + ServerAlive from OpenSSH.
4. On success, attach the same session name. Remote tmux never stopped.

## Workspace model

Keep it small.

```
Machine        ssh config alias (devbox)
  Multiplexer  tmux (only backend in MVP)
    Session    backend
      Window   remote tmux owns these
        Pane   remote tmux owns these
```

Identity:

```
tmux://<machine>/<session>
```

Examples: `tmux://devbox/backend`, `tmux://gpu01/training`.
Display form: `devbox/backend`.

Rules:

- `<machine>` is the Hive host id, usually an SSH config `Host`.
- `<session>` is the **name**, not `$id`. IDs recycle; names are what
  people attach to.
- Optional query: `?socket=foo&window=2` later. Not MVP.
- Generation token (not part of the URI): `pid:session_created`. Used
  only to detect “same name, different server life.” Surface a warning;
  do not silently refuse the way tmax does after a reboot if the user
  clearly wants `backend`.

A workspace exists whether or not an SSH connection exists.

## SSH connection lifecycle

```
idle host
    → no process

picker refresh / first attach
    → ssh -o ControlMaster=auto
      -o ControlPersist=120
      -o ControlPath=$XDG_RUNTIME_DIR/hive/<host>.sock
      …   # mux master

additional list-sessions / ctl
    → ssh -S <master> -O check / new mux channel

user attaches
    → dedicated PTY channel on the same master
      (or a new ssh -t if that is more reliable; measure)

workspace detached and no other workspaces on that host
    → close PTY
    → ControlPersist lets the master die after 120s

all hosts idle
    → no SSH processes
```

Do not hold a connection for an inactive workspace. Do not open a
connection to list hosts — the host list is local config.

Health: OpenSSH `ServerAliveInterval=15`, `ServerAliveCountMax=2`
(~30s dead-peer). Hive’s supervisor is the second layer (backoff,
resume workspace). Sleeping laptops: treat resume like any other
network return; do not special-case Wake-on-LAN in MVP.

Multiple sessions on one host share the master. Multiple panes/windows
of one session share **one** PTY, because remote tmux draws them.

ProxyJump, agent, PKCS#11, `IdentitiesOnly`, `CanonicalizeHostname`:
all via OpenSSH. Resolve hosts with `ssh -G <alias>` rather than
re-parsing `~/.ssh/config`.

## tmux connection lifecycle

```
discover:  short mux exec, tmux list-sessions
create:    mux exec, tmux new-session -d -s name
attach:    PTY, tmux attach-session -t =name
sidecar:   opened only if we need events/commands that cannot go
           through the PTY (post-MVP or Sessh-parity)
detach:    client prefix → detach-client or just kill the SSH PTY
           (remote session remains)
kill:      mux exec, tmux kill-session -t =name
```

Remote tmux stays a completely normal server. Other clients (another
Hive, a raw SSH, Sessh, iTerm `-CC`) can attach at the same time.
`window-size latest` (Sessh already does this) so a forgotten 80x24
phone client does not pin the layout.

## Session lifecycle

```
exists on remote  →  appears in index (online)
selected          →  attached workspace
client prefix     →  picker; session still running
SSH dies          →  workspace = reconnecting; session still running
session killed    →  workspace gone; return to picker
tmux server dies  →  name may reappear later as a new generation
```

## Reconnect lifecycle

Local state that must survive:

```
last_workspace   tmux://devbox/backend
last_window      optional index, best-effort
client_size      cols x rows
host_cache       sessions + status + fetched_at
```

Not required: pane IDs, layout strings, PTY fds, SSH sockets, scrollback
(remote tmux has it).

Algorithm:

1. Persist `last_workspace` before attach and on each successful attach.
2. On I/O error, mark workspace reconnecting; do not clear identity.
3. Backoff 1s, 2s, 4s, … cap 30s. Reset on success.
4. `ssh -O check` then attach. If master is dead, open a new one.
5. If `has-session` fails, stay on that workspace with “session gone”
   and offer recreate / pick another. Do not walk the user through
   rediscovery.

## Failure modes

| Failure | Behaviour |
|---|---|
| Host offline | Cache remains; badge offline; no attach |
| Auth failed | `auth required`; do not retry in a tight loop |
| Host key changed | OpenSSH fails; show stderr; do not auto-accept |
| tmux missing | Host online, no sessions, explicit error |
| Session missing | Recreate or pick |
| SSH drop while attached | Reconnect same workspace |
| Split-brain name reuse | Warn with generation token |
| Local terminal resize during backoff | Apply on next attach |
| Two Hive clients on one session | Allowed; remote tmux multiplexes |

## Security model

Trust boundary is OpenSSH. Hive is a supervisor of `ssh` and a parser of
tmux text.

- No private keys stored. Agent / hardware keys / `IdentityFile` from
  config only.
- No remote daemon, no listen port, no registry service.
- Host list is user config (`hive.toml` + SSH aliases), not a scan.
- Remote commands are fixed: `tmux` plus quoted arguments. No `sh -c`
  of user-typed host names. Host ids must match `[A-Za-z0-9_.-]+`.
- Session metadata (names) may leak project names into local logs.
  Keep logs local, `0700` runtime dir (borrow tmax’s check).
- Threat model: a compromised Hive process is equivalent to “user can
  run ssh.” A compromised remote is unchanged (already has the session).
  A malicious session name must not break out of the TUI or the ssh
  argv. Do not implement an unauthenticated LAN discovery protocol.

Do not use `russh` / `x/crypto/ssh` as the production transport. They
do not implement `~/.ssh/config`, ProxyJump, `Match exec`, security
keys, or `known_hosts` the way OpenSSH does. In-process SSH is a later
optimisation, not a security improvement.

---

# Deliverable 3 — architectural comparison

| | tmax | Hive (proposed) |
|---|---|---|
| **Global session model** | Local tmux is the universe. Remotes are imported into it as `host/name`. | Client-owned index. URI identity independent of any local mux. |
| **Local tmux dependency** | Required. Plugin cannot run without it. | Not required. Can run in Ghostty/kitty/iTerm as a normal program. Optional later integration. |
| **Remote representation** | Local proxy session + one `view` pane per remote pane. | Pointer + one normal tmux client (PTY). Remote objects stay remote. |
| **SSH connections** | ControlMaster per host; **one channel per opened pane** plus fetch/watch. | ControlMaster per host; **one PTY per attached workspace**; short mux execs for discovery. Inactive workspace → no connection. |
| **tmux control mode** | Primary I/O path (`-C attach` per pane, `send-keys -H`, `%output`). | Sidecar only. Not the interactive path. |
| **Terminal rendering** | Remote tmux captures; Python forwards; **local tmux emulates again**. Copy-mode is local and starts empty. | Remote tmux renders once, as today. Copy-mode, scrollback, layouts, status bar are native. |
| **Reconnect** | Per-pane 5s retry; input discarded; epoch may refuse reused IDs; lost if local tmux dies. | Client-owned workspace pointer; backoff; persist last workspace; local tmux crash irrelevant. |
| **Performance** | Extra hop and extra processes. Input command-RTT. `%output` tax on high-output panes. | Attached path ≈ `ssh + tmux attach`. Discovery is extra and idle. **tmax is worse here.** |
| **Complexity** | High accidental complexity (layout translate, binding rewrite, placeholder windows, Unix sockets into `view`). Low conceptual surface if you already use tmux. | Lower runtime complexity. Must build a picker + PTY supervisor + a client prefix that does not fight the remote prefix. |
| **Remote requirements** | SSH + tmux 3.4+. Nothing installed. | SSH + tmux (3.2+ is enough for attach/list). Nothing installed. **Tie, slightly fewer version constraints.** |
| **Extensibility** | Stuck behind local tmux bindings and proxy fidelity. Hard to add Mosh, files, agents, a second mux. | Natural: transport (SSH/Mosh), mux (tmux/zellij), Sessh, metadata. |
| **UX if you live in local tmux** | **tmax is better.** One prefix, one tree, remotes look local. | You leave local tmux (or run Hive inside one pane) and use a client prefix to switch worlds. |
| **UX if you work across many machines** | Proxies accumulate. Nested prefix rules. Overview does not work remotely. | One picker for everywhere. Attached session feels like the session you already have. |

tmax is better as a **local-tmux companion**. Hive is better as a
**workspace client**. Those are different products. Hive should not
pretend it is a faster tmax.

Is the direct architecture actually better? Yes, on performance,
simplicity of the data path, reconnection, and extensibility — **if**
we attach as a normal tmux client. A `-CC`-rendering Hive would likely
be worse than tmax on complexity and not better on latency.

---

# Deliverable 4 — technology recommendation

## Recommend Go

The MVP is process supervision, OpenSSH, a fuzzy TUI, and a raw PTY
passthrough. That is Go’s home turf. Rust wins if we later build an
in-process VT emulator and a control-mode renderer. We should not do
that first, so we should not pick the language for that first.

### Why Go

- `os/exec` + goroutines map directly to ConnManager / AttachSupervisor
  / per-host refresh.
- TUI: [bubbletea](https://github.com/charmbracelet/bubbletea),
  [bubbles](https://github.com/charmbracelet/bubbles) (list, textinput,
  viewport), [lipgloss](https://github.com/charmbracelet/lipgloss),
  [huh](https://github.com/charmbracelet/huh) if we need forms.
- PTY: `github.com/creack/pty` or just `ssh -t` (OpenSSH allocates the
  remote TTY; locally we raw the stdin).
- Terminal: `golang.org/x/term` for raw mode and size.
- SSH: **OpenSSH binary**, not `golang.org/x/crypto/ssh`.
- Control-mode parser (sidecar): small line scanner. No need for a
  parser-combinator stack.
- Cross-compile macOS/Linux, static-ish binaries, fast iteration.
- This repo’s surrounding work is already Go-heavy.

### Why not Rust for MVP

Rust is the better choice for a second implementation of iTerm2’s
control-mode client (alacritty `vte`, wezterm mux, ratatui). That
product needs a real emulator per pane, layout math, and careful
backpressure. Hive MVP does not. Starting in Rust “for performance”
optimises a path we are deliberately not taking.

If a sidecar `%output` preview ever becomes hot, that crate can be
Rust. The supervisor can stay Go.

### Async model

Go: one goroutine per host refresh, one per attached PTY copy
(`io.Copy` both directions), one for the TUI. No async runtime debate.

A `select` on SSH wait, SIGWINCH, client-prefix detector, and reconnect
timer is enough.

### Terminal handling

Attached mode is not a TUI frame. It is a passthrough. The user’s
emulator (Ghostty, kitty, iTerm, Terminal.app) does Unicode, truecolor,
and mouse. Hive must:

- set raw mode
- forward bytes unchanged
- forward resize
- steal one key chord for the client prefix

Picker mode is bubbletea. Do not try to keep a three-column dashboard
**and** a full-screen tmux attach at once in MVP. The attached session
already has panes.

### SSH implementation

```
ssh -G <host>                         # resolve config
ssh -O check -S <ctl> <host>          # master health
ssh -o ControlMaster=auto …           # open master
ssh -t -S <ctl> <host> -- tmux …      # attach
ssh -T -S <ctl> <host> -- tmux …      # discovery / sidecar
```

Inherit `SSH_AUTH_SOCK`. Do not wrap `ssh-agent`. Honour `IdentitiesOnly`.

### tmux protocol implementation

MVP: argv + `-F` formats. No control-mode parser required.

Later sidecar: line-oriented `%begin` / `%end` / `%error` /
`%sessions-changed` / `%window-add`. Use IDs in commands, names in URIs.
Enable `pause-after` if we ever subscribe to `%output`. Never send
interactive keystrokes via `send-keys -H`.

### Expected performance

Attached: indistinguishable from `ssh -t host tmux attach`. That is the
goal, not a benchmark suite.

Picker: cached paint <50ms; per-host refresh is one RTT and never
blocks keystrokes (tmax already proved this pattern).

Memory: one SSH PTY and a session list. Not one process per pane.

### Development complexity

Small. A competent Go TUI for picker + exec attach is weeks, not a
control-mode client (iTerm2 spent years). Tests: isolated `tmux -L`
sockets locally; optional SSH integration like tmax’s
`remote_integration_test.py`.

---

# Deliverable 5 — MVP

Smallest useful product:

```
configured hosts
    → cached + parallel list-sessions
    → fuzzy picker (machine + session)
    → ssh -t host tmux attach
    → client prefix returns to picker
    → SSH death reconnects the same workspace
```

## In

1. `hive.toml` listing hosts (`ssh` alias, optional label, optional
   `tmux` path, optional socket). No auto-import of every `Host *`.
2. Disk cache of last session lists and last workspace.
3. Full-screen fuzzy picker. Filter on `machine`, `session`, or
   `machine/session`. Offline hosts stay visible.
4. Enter on a match: attach. Enter on no match: `new-session -d` on the
   selected/default machine, then attach.
5. PTY attach with `tmux -u`, exact name (`-t =session`).
6. Client prefix (default `Ctrl-Space`) → detach PTY, show picker,
   highlight current workspace. Remote session stays up.
7. Reconnect supervisor with backoff and a one-line status.
8. Create / kill / rename from the picker (confirm on kill).
9. macOS and Linux. OpenSSH + tmux on both ends.

## Out

- Local proxy sessions
- Control-mode rendering
- Sidebar / three-column dashboard
- Groups, tags, Git, process lists, machine health
- Mosh / Eternal Terminal
- Remote file browsing
- AI agent chrome
- Sessh integration
- tmux plugin
- In-process SSH
- Persistent connection pools beyond ControlPersist
- Host auto-discovery / Tailscale API
- Multiple multiplexer backends

## UI

Do not ship the three-column mock as MVP. It spends columns on
navigation that a fuzzy finder does better, and it fights the attached
tmux layout.

**Picker:** fullscreen list, vim-ish movement, tmax-style immediate
cache + background reload. Host badge, window count, online/offline.

**Attached:** the terminal is the terminal. A single status line only
while reconnecting.

**Client prefix vs remote prefix:** Hive steals one chord. Remote tmux
keeps its prefix. Document the default and make it configurable. This
is the one UX cost of not living inside local tmux. It is cheaper than
tmax’s partial binding translation.

## Interaction research (what we are not building yet)

| Job | MVP | Later |
|---|---|---|
| Find anything | fuzzy on `host/session` | tags, git branch, pane cmd |
| Filter by machine | query `devbox/` | machine column |
| Attach / detach / switch | enter / client prefix / enter | last-N stack |
| Create / kill / rename | picker actions | — |
| Windows / panes | remote tmux, as today | optional sidecar grid (Sessh already has this) |

High-latency: never block the filter on SSH. Never refresh the list
under the cursor unless the selected id disappeared (tmax `--track`
pattern).

---

# Deliverable 6 — roadmap

Clearly not MVP.

| Later | Why |
|---|---|
| Richer workspace metadata | `#{pane_current_command}`, activity, attached-elsewhere |
| Git information | `#{pane_current_path}` + one mux exec; cache hard |
| Process / machine health | tempting, easy to become a bad monitoring product |
| Persistent connection pools | only after measuring ControlPersist misses |
| Tags / groups | tmax sidebar groups; store locally, namespaced by URI |
| Workspace groups | “work” = `devbox/backend` + `gpu01/training` |
| Multiple mux backends | zellij/abduco; same URI with a different scheme |
| Remote file browsing | Sessh already has this; reuse the idea, not the UI |
| AI-agent sessions | Sessh agent windows; detect via pane command |
| Control-mode sidecar | live `%sessions-changed`, previews via `capture-pane` |
| Control-mode renderer | iTerm-like native panes; only if PTY attach is proven
and we still need client-drawn layout. Separate project. |
| Optional tmux plugin | `prefix + Space` opens Hive or lists Hive URIs. Does
not proxy remotes. |
| Mosh / ET | unstable links. Mosh and `-CC` do not mix; PTY attach
does. |
| Sessh | same URIs, same attach command, same host ids. Hive is
the desktop picker; Sessh is the mobile client. Transport
chooser (`tmux` / raw SSH / Mosh / agent) lives there. |

---

# Control mode — what it can and cannot do

From tmux wiki + `control.c` (not from tmax’s README).

## Can

- Attach as a text client (`tmux -C` / `-CC attach-session`).
- Run any normal tmux command; results wrapped in `%begin`/`%end` or
  `%error`.
- Receive `%output` / `%extended-output` for application bytes in panes
  (octal-escaped, `TERM=tmux`/`screen` sequences).
- Notifications: session/window/pane add/close/rename/change,
  `%sessions-changed`, `%pane-mode-changed`, …
- Resize: `refresh-client -C WxH` or per-window `@id:WxH`.
- Flags: `no-output`, `ignore-size`, `pause-after=N`, `read-only`,
  `active-pane`, `wait-exit`.
- Per-pane `:on` / `:off` / `:pause` / `:continue`.
- Format subscriptions (`refresh-client -B`), at most 1 Hz.
- `send-keys -H` for literal bytes, bypassing the remote prefix.
- `capture-pane -p -e` for a snapshot (tmax `repaint`).

## Cannot / must not rely on

- **tmux-drawn UI is not sent.** Copy-mode, choose-tree, menus, the
  status line tmux itself draws — not in `%output`. A `-CC` client must
  reimplement them or give them up.
- **Scrollback is not streamed.** History lives in the server. Import
  requires `capture-pane -S -` (large, still not a live buffer).
- **Input via `send-keys` is a command**, not a PTY write. Latency and
  buffering differ from typing into `attach`.
- **Without `pause-after`**, a slow client is killed at 300s
  (`CONTROL_MAXIMUM_AGE`). **With it**, output is discarded and the
  client must snapshot.
- **`-CC` DSC handshake** is for listening terminals (iTerm2). A pipe
  client should use `-C`.
- **Layouts** arrive as strings; the client must parse and draw them
  (iTerm2 `TmuxLayoutParser`; tmax `layout_translate`).
- **Environment / `TERM`:** control clients often want `-E` so they do
  not clobber the session. Applications still see the pane’s `TERM`.
- **Mosh:** needs a predictive PTY; control mode is a reliable
  line protocol. Do not combine them.

Conclusion: control mode is an excellent **API**. It is a poor
**terminal**. Hive should use the API later and the terminal (normal
attach) now.

---

# Client vs plugin vs hybrid

| | Plugin (tmax) | Standalone client (Hive) | Client + optional plugin |
|---|---|---|---|
| Global model | local tmux | client | client; plugin is a launcher |
| Constrained by | bindings, local copy-mode, proxy fidelity | must own picker + prefix | plugin cannot fix those constraints |
| Works without local tmux | no | yes | yes |
| Feels like “one tmux” | yes | no | only locally |
| Feels like “one workspace client” | no | yes | yes |
| Sessh-shaped | no | yes | yes |

**Decision: standalone client.** Optional plugin later as
`prefix + Space → hive` or a choose-tree of URIs. Do not use the plugin
to proxy remotes. That would be tmax.

What local tmux-as-UI fundamentally cannot do well:

- Survive local tmux death as a workspace pointer.
- Represent remotes without either proxies or a nested `ssh && attach`
  pane (which is just Hive-without-Hive).
- Translate every command.
- Give you remote scrollback in local copy-mode without importing it.
- Be the same client on a phone (Sessh).

---

# Sessh

Sessh already:

- lists sessions with `tmux list-sessions -F` (`src/tmux/service.ts`)
- attaches with a PTY and `tmux attach-session -t =name`
  (`attachCommand`)
- keeps a warm non-PTY helper for commands (`src/tmux/ctl.ts`) so
  `prefix + :` is not raced
- reconnects at the connection/session-manager layer
- has host-local session/window/pane switchers

Hive is that architecture with a **global index**. Shared later:

```
tmux://devbox/backend
```

same attach argv, same host ids, different UI toolkit.

Transport chooser (SSH / Mosh / agent) stays a Sessh concern. Hive MVP
is SSH + tmux only.

Do not fold Hive into the Expo app. Keep a Go binary that Sessh can
call or whose URI scheme it can implement.

---

# Guiding test

After attach, Hive should disappear except for reconnect and a prefix
that brings back the picker.

It should feel like: *my sessions are everywhere; I have one client.*

It should not feel like: *my laptop’s tmux is full of fake sessions
that proxy other machines.*

If a design requires local proxy sessions or `send-keys -H` for typing,
it is the wrong design.
