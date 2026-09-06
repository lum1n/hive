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

Password hosts: open a master first and set `control_path`.

```sh
ssh -M -S ~/.ssh/hive-laptop.sock -o ControlPersist=30m -fnN user@laptop
```

Override the file with `-config` or `HIVE_CONFIG`.

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
| attached | ctrl-space | detach Hive, leave remote tmux running |
| reconnect | q | back to picker |

Remote tmux keeps its own prefix.

## How listing stays fast

The picker paints the last snapshot from `~/.local/state/hive/` immediately,
then refreshes hosts in parallel with `ssh -T … tmux list-sessions`. The first
refresh also opens a ControlMaster so attach does not handshake again.
