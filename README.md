# hive

One picker for tmux sessions across all your machines. Remote tmux stays a
normal tmux server: hive lists sessions over SSH, you pick one, and it attaches.
`ctrl-space` brings you back to the picker.

```
configured hosts → cached + parallel list-sessions → fuzzy picker → attach
```

It also finds coding agents (Claude Code, Codex, Copilot CLI, Cursor Agent,
OpenCode, Pi) running in tmux panes on those hosts, and jumps straight to them.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/lum1n/hive/master/install.sh | sh
```

Installs to `~/.local/bin/hive` (override with `BINDIR=/usr/local/bin`).
Linux and macOS, amd64 and arm64.

`go install github.com/lum1n/hive/cmd/hive@latest` also works if you have
Go 1.26+, but it compiles from source.

From a clone: `make install`.

Needs OpenSSH locally, and tmux on every host you list. Nothing is installed on
remote hosts.

## Quick start

```sh
hive -init   # writes ~/.config/hive/hive.toml with your local machine
hive         # open the session picker
```

Add remote hosts to `~/.config/hive/hive.toml`:

```toml
prefix = "ctrl-space"

[[hosts]]
id = "local"
label = "local"
local = true

[[hosts]]
id = "devbox"
ssh = "devbox"     # any OpenSSH destination: alias or user@host
```

## Usage

```sh
hive                    # session picker
hive agents             # agent picker
hive agents --host box  # agents on one host
hive -dump              # print host status and sessions, no TUI
hive version
hive --help
```

### Session picker

| Key | Action |
|---|---|
| type | filter (`devbox/back` or `backend`) |
| arrows / `ctrl-j` `ctrl-k` | move |
| `enter` | attach, or create if nothing matches |
| `ctrl-n` | new session on the selected host |
| `ctrl-r` | rename |
| `ctrl-x` | kill (confirm with `y`) |
| `esc` | clear filter, then quit |
| `ctrl-c` | quit |

### Agent picker

Rows show host/session, window, agent kind, state, and project path. Hosts
fill in as they answer, so one slow host never blocks the rest.

| Key | Action |
|---|---|
| type | filter by host, session, window, kind, pane, or path |
| arrows / `ctrl-j` `ctrl-k` / `pgup` `pgdn` | move |
| `enter` | attach to the exact agent pane |
| `ctrl-r` | refresh |
| `esc` | clear filter, then quit |
| `ctrl-c` | quit |

### While attached

| Where | Behavior |
|---|---|
| plain terminal | `ctrl-space` detaches hive and returns to the picker; remote tmux keeps running |
| inside tmux, local session | hive runs `switch-client` instead of nesting tmux; switch back with your usual tmux keys |
| inside tmux, remote session | the outer prefix and status bar are suppressed so the remote tmux is in control |

If SSH drops, hive reconnects to the same workspace.

## Configuration

`~/.config/hive/hive.toml`, or `$XDG_CONFIG_HOME/hive/hive.toml`. Override
with `-config PATH` or `HIVE_CONFIG`.

| Key | Default | Meaning |
|---|---|---|
| `prefix` | `ctrl-space` | detach key |
| `agent_watcher` | `auto` | read agent states from a running watcher; `off` disables |
| `control_persist` | `120` | seconds an idle SSH master stays open |

Per `[[hosts]]`:

| Key | Meaning |
|---|---|
| `id` | short name used in the picker and on the CLI |
| `label` | display name (defaults to `id`) |
| `local` | `true` for the machine you sit on — never SSH to yourself |
| `ssh` | OpenSSH destination (defaults to `id`) |
| `tmux` | tmux binary; only pin a path if a host has two installs |
| `socket` | specific tmux socket |
| `control_path` | existing SSH master socket (for password hosts) |

hive uses your SSH agent, `~/.ssh/config`, ProxyJump, and `known_hosts`. It
never stores keys. Hosts are opt-in; hive does not scan `~/.ssh/config`.

Over SSH, hive finds tmux wherever it was installed (Homebrew, MacPorts, Nix,
asdf/mise, conda, …) and locates sockets under `/tmp` and the macOS GUI temp
dir.

For hosts that need a password, open a master first and point `control_path`
at it:

```sh
ssh -M -S ~/.ssh/hive-laptop.sock -o ControlPersist=30m -fnN user@laptop
```

## Agents API

Everything in the agent picker is also available headless, as JSON:

```sh
hive agents list --json
hive agents capture --id '<id>' --lines 200 --json
hive agents attach --id '<id>'
hive agents capabilities --json
```

Mark a wrapper process as an agent, or hide a pane:

```sh
tmux set-option -p -t %12 @agent-overview-kind copilot
tmux set-option -p -t %13 @agent-overview-kind off
```

Schemas, limits, exit codes, and the optional watcher bridge are in
[docs/agents.md](docs/agents.md).

## How it stays fast

The picker paints the last snapshot from `~/.local/state/hive/` immediately,
then refreshes every host in parallel over a shared SSH ControlMaster. Attach
opens its own SSH TTY.

Design notes: [docs/architecture.md](docs/architecture.md).

## License

MIT
