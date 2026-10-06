# Hive

One client for tmux sessions across machines. Remote tmux stays a normal
tmux server. Hive discovers sessions, you pick one, then it runs
`ssh -t host tmux attach`.

```
configured hosts
    → cached + parallel list-sessions
    → fuzzy picker
    → PTY attach
    → ctrl-space returns to the picker
    → SSH death reconnects the same workspace
```

Design: [`docs/architecture.md`](docs/architecture.md).

## Install

```sh
go install github.com/lum1n/hive/cmd/hive@latest
```

Or from this repo:

```sh
go build -o hive ./cmd/hive
```

Needs a local terminal, OpenSSH, and tmux on each machine you list.

## Config

```sh
hive -init
```

writes `~/.config/hive/hive.toml`:

```toml
prefix = "ctrl-space"

[[hosts]]
id = "local"
label = "local"
local = true
tmux = "tmux"

[[hosts]]
id = "devbox"
ssh = "devbox"
tmux = "tmux"
```

`ssh` is an OpenSSH destination (alias or `user@host`). Hive uses your
agent, `~/.ssh/config`, ProxyJump, and `known_hosts`. It does not store keys.

The machine you sit on must be `local = true`. Listing the same Mac over
`ssh = "macbook"` talks to a different tmux socket (GUI `TMPDIR` vs `/tmp`)
and a PATH that often lacks Homebrew, so Hive used to report the host
online with no sessions.

Over SSH, Hive finds `tmux` wherever it was installed — Homebrew (Apple
Silicon, Intel, Linuxbrew, keg-only, Cellar), MacPorts, Fink, pkgsrc,
Nix, Guix, Snap, asdf/mise, conda — prefers the binary of a live server,
then locates sockets under `/tmp`, `/private/tmp`, and the macOS GUI temp
dir. Set `tmux = "/explicit/path/tmux"` only to pin a specific binary.

Password hosts: open a master first and set `control_path`.

```sh
ssh -M -S ~/.ssh/hive-laptop.sock -o ControlPersist=30m -fnN user@laptop
```

Override the file with `-config` or `HIVE_CONFIG`.

`hive -dump` prints each host’s status and sessions without the TUI.
`hive -version` prints the build.

## Headless agent API

Hive can discover Copilot CLI, Claude Code, Codex, Pi, OpenCode, and Cursor
Agent panes on configured hosts without opening the picker:

```sh
hive agents capabilities --json
hive agents list --json
hive agents list --host devbox --json
hive agents capture --id '<id from listing>' --lines 200 --json
hive agents attach --id '<id from listing>'
```

Repeat `--id` to capture up to 16 agents in one batch. Requests are grouped by
host and reuse SSH connections. Each reference identifies a host, canonical
tmux socket, server generation, and pane; renames do not invalidate it, but
server restarts do. Attachment selects the exact pane. Inside the same local
tmux server, it switches the initiating client instead of nesting tmux; use
`--client` when multiple clients make the origin ambiguous.

Remote hosts need only their existing SSH, tmux, and standard OS tools.
Neither Hive nor Python needs to be installed remotely. Detection supports
interpreter wrappers and process descendants. An explicit pane override can
mark a wrapper or exclude a pane:

```sh
tmux set-option -p -t %12 @agent-overview-kind copilot
tmux set-option -p -t %13 @agent-overview-kind off
```

Agent listing is metadata-only and initially reports `unknown`. Captures
provide conservative heuristic states, not authoritative agent-watcher state.
These commands do not use the picker cache or write previews to disk.
`capabilities` does not read configuration or contact hosts. Other commands
contact only configured hosts; `--host` narrows listing.

JSON commands exit 0 on success, 2 for partial/operational failures with a
complete JSON response, and 1 for invalid requests/configuration. Diagnostics
are on stderr. Flags such as `--config` and `--timeout` can follow the agent
subcommand; existing global flags still work before `agents`.

See [the version-1 agent contract](docs/agents.md) for schemas, bounds,
failure codes, and integration guidance. The default session picker and
`hive -dump` remain unchanged; an interactive agent picker is not included.

## Keys

| Where | Key | Action |
|---|---|---|
| picker | type | filter (`devbox/back` or `backend`) |
| picker | arrows / ctrl-j/k | move |
| picker | enter | attach, or create if nothing matches |
| picker | ctrl-n | new session on the highlighted host |
| picker | ctrl-x | kill (confirm y) |
| picker | ctrl-r | rename |
| picker | ctrl-c | quit |
| attached (not inside tmux) | ctrl-space | detach Hive, leave remote tmux running |
| attached (inside tmux, local session) | — | Hive runs `switch-client` (no nested tmux). Switch back to the Hive pane with your usual tmux session switcher. |
| attached (inside tmux, remote session) | ctrl-space | outer prefix/status are suppressed so the remote tmux is in control |

Remote tmux keeps its own prefix. Do not `tmux attach` from inside another tmux client — Hive avoids that nest for local sessions.

## How listing stays fast

The picker paints the last snapshot from `~/.local/state/hive/` immediately,
then refreshes hosts in parallel with `ssh -T … tmux list-sessions`. Listing
reuses a ControlMaster; attach opens a fresh SSH TTY because a no-TTY mux
cannot allocate a PTY for tmux.
