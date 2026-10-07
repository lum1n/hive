# Headless agent API, version 1

This interface is intended for callers such as tmux-agent-overview. It uses
the existing configured hosts, OpenSSH transport, socket discovery, and PTY
attachment. It does not require a remote Hive installation or daemon.

## Commands

| Command | Purpose |
| --- | --- |
| `hive agents capabilities --json` | Network/configuration-free feature and limit discovery |
| `hive agents list --json [--host ID]` | Fresh metadata inventory across configured hosts |
| `hive agents capture --json --id REF [--id REF ...] [--lines 200]` | Bounded batch capture of selected agents |
| `hive agents attach --id REF [--client CLIENT]` | Interactive exact-pane attachment |

`hive agents` and `hive agents list` without `--json` open the interactive
agent picker. `hive agents --json` is an alias for `hive agents list --json`.
The picker shares the session picker's appearance and fuzzy matching, discovers
hosts incrementally with at most four concurrent operations, and keeps metadata
and selection in memory only. Enter attaches to the selected exact reference;
the configured detach key returns from PTY attachment to the picker.
Ctrl-r refreshes; esc clears the filter or exits; ctrl-c quits.
No match never creates a session, and session rename/kill shortcuts are disabled.
`--host ID` restricts discovery; `--client CLIENT` disambiguates local focus.

Every command accepts `--config PATH` and `--timeout DURATION`. Timeout applies
to each host operation, defaults to 12 seconds, and must be positive and at
most 60 seconds. Global `-config` also works before `agents`. JSON commands
use `--json`; capture and capabilities require it. Interactive listing and
attach require a terminal; attach cannot emit JSON.
`hive --help`, `hive agents --help`, and `hive agents <command> --help`
show usage and examples without loading configuration, opening a terminal UI,
or contacting hosts. `-h` is an alias; `hive agents --help` shows group help.

The capabilities response includes `version`, `commands`, `kinds`,
`max_targets`, `max_lines`, `max_bytes`, and `state_sources` (`heuristic`, `shared`).
Consumers must reject unsupported
major versions. Additional response fields may be added within version 1.
Do not parse human diagnostics, `hive -dump`, or Hive's private cache files.

## Inventory

The list envelope contains `version: 1`, UTC `time`, and `hosts`.
Each host contains its configured `host` ID, display `label`, `local` flag,
`status`, optional `error`, and `servers`. Host status is `online`, `degraded`,
`offline`, `auth`, or `unavailable`. Servers contain `socket`, `generation`,
`status`, optional `error`, and `agents`.

An agent contains:

| Field | Meaning |
| --- | --- |
| `id` | Opaque, versioned locator passed unchanged to capture/attach |
| `host`, `socket`, `generation`, `pane` | Server-qualified identity |
| `window`, `session` | One current membership, not immutable identity |
| `memberships` | All linked session/window memberships, with IDs and display names |
| `kind` | `copilot`, `claude`, `codex`, `pi`, `opencode`, or `cursor` |
| `path` | Sanitized project/directory display metadata |
| `state`, `provenance` | Existing watcher state with `shared` provenance, otherwise `unknown`/`unavailable` |
| `state_error` | Optional content-free watcher lookup error; not an inventory failure |
| `observed_at` | UTC metadata observation time |

Host/server collections and agent collections are arrays, including when
empty. Offline or failed discovery is never represented as a healthy empty
inventory. Successfully queried servers with no detected agents have an empty
`agents` array. Linked panes occur once per server, with multiple memberships.
Discovery uses two batched pane-metadata reads per server, not per-pane tmux
requests. Metadata is length-framed and encoded, so Unicode, tabs, newlines,
commas, and shell-like labels cannot corrupt the inventory protocol.
Probes explicitly request UTF-8 tmux output, including over SSH with a `C`
or unset locale. Otherwise tmux can replace Unicode labels with underscores,
invalidating metadata byte lengths and losing captured text.
Socket discovery skips `*.agent-watcher.sock` control sockets and prefers
live-process socket discovery over directory sweeps when available. Watcher
control sockets speak a different protocol and must not be queried as tmux
servers.

References are locators, not credentials. Hive validates their fields,
resolves only configured hosts and same-user discovered/pinned sockets, and
checks the server PID/start-time generation. Identical pane IDs on different
hosts/servers are distinct. Renaming or moving a pane within its server keeps
its identity; removal or server restart requires rediscovery.

## Captures and states

The capture envelope contains `version: 1` and `captures`, in request order.
Each result contains `id`, UTC `time`, `state`, `provenance`, `truncated`,
optional `text`, optional `error`, and optional `state_error`. Failed captures have no preview text
and remain `unknown`/`unavailable`. Returned IDs match the requested opaque
references, including on failure.

Capture requests are grouped by host, with at most four concurrent host
operations. The probe resolves candidate sockets once per host batch and
checks each target's generation before and after capture. One failed target
does not invalidate successful targets in a valid batch response.

Only running/detected or explicitly marked panes are exposed. Terminal
escapes and unsafe control characters are removed. Captures retain up to the
requested number of recent lines after trailing blank terminal rows are
removed, with at most 64 KiB of returned text. `truncated` indicates byte
clipping. Excessive source dimensions (over 4096 columns or 512 rows) are
rejected rather than allowing unbounded remote shell buffering.

States are `unknown`, `idle`, `thinking`, `running-tool`,
`waiting-permission`, or `errored`. Successful captures use `shared` provenance
when safely matched to an existing watcher, otherwise `heuristic`.
Inconclusive output remains `unknown`; listing never captures panes or infers
idle from missing evidence.

### Optional watcher bridge

Default `agent_watcher = "auto"` discovers `@agent_watcher_socket` on each tmux
server, falling back to `<server socket>.agent-watcher.sock` when not configured.
It uses the existing Python 3 runtime on that host for a short-lived, read-only
Unix-socket snapshot request through the same SSH transport. Neither Hive nor
a new daemon is installed remotely. No watcher/Python installation is required
for normal Hive operation. Set `agent_watcher = "off"` in the global TOML section,
or pass `--no-watcher` to list/capture, to skip lookups entirely. Attachment
resolution does not request watcher states.

Socket ownership, private permissions, and safe parent directories are verified; symlinks and foreign or
unsafe sockets are rejected. Linux peer credentials are also checked. Only
version-1 `hello`/`snapshot` frames are accepted, with a 1 MiB byte limit,
32 events, and 4096 records. Raw watcher payloads remain in memory and are not
exported or logged; only validated state/kind/identity matches are returned.

Watchers currently identify a session name and window index, not a pane. Hive
therefore applies a record only to an active, single-pane window whose current
kind, pane PID, and window identity match its inventory/capture. Linked windows
are deduplicated by pane; conflicting linked states are not applied. Split
windows, unbound records, and absent/nonmatching records retain the normal
fallback. Server generation is checked around snapshot/metadata reads.

Watcher reads have a 0.8-second socket deadline and a separate 1.2-second host
batch budget, also constrained by the normal per-host deadline. A stalled,
malformed, unsafe, disconnected, or unsupported watcher sets `state_error`
with an existing error code and a content-free message. It never discards a
successful inventory/preview or changes its primary `error`/exit status.
Missing unconfigured companion sockets are normal standalone operation.

## Limits and failures

At most 16 unique references and 1-500 lines are accepted per capture request.
Listing accepts up to 64 hosts, 64 server candidates per host, and 4096 pane
membership records per host. A host process snapshot is limited to 8192
process records; targeted interpreter arguments are limited to 128 processes
and 8192 bytes each. Probe output is capped at 4 MiB and stderr at 8 KiB.
Interpreter arguments are inspected only for `node`, `bun`, `deno`, and
Linux Node.js `MainThread` processes descended from eligible panes, not
unrelated host processes. Generic `MainThread` processes are not agents:
their arguments must identify a supported agent module.
Excluded, dead, and plugin-owned panes are not argument-inspection roots.
Bubblewrap (`bwrap`) launchers are followed through their host-visible
parent/child process relationships. Detection does not enter the sandbox
or require access to paths inside its filesystem.

Errors contain `code` and a bounded, content-free `message`. Codes include
`auth`, `offline`, `timeout`, `cancelled`, `unavailable`, `discovery`,
`protocol`, `output_limit`, `stale`, and `gone`. No command arguments,
transcripts, raw remote output, or credentials are included in diagnostics.

JSON commands exit 0 for complete success, 2 for any host/server/capture
operational failure while still emitting the complete response, and 1 for
invalid input, configuration, or output-write errors. Attach exits nonzero on
failure. SSH disconnection retries use the existing bounded backoff; a stale
or missing target is not silently replaced. Ctrl-space detaches without
terminating the agent; `q` during reconnect cancels reconnect.

## Integration and privacy

Consumers should explicitly enable remote discovery, capability-check Hive,
retain freshness/status information, and capture only visible panes at a
bounded cadence. Use slower inventory polling and batch visible previews.
Do not treat a failed host as zero running agents or replay raw terminal
control sequences.

Remote attach needs a real interactive pane with normal key handling. It
must not run in a read-only empty preview tile or a controller subprocess
without a terminal. Local same-server attach switches only the resolved
initiating client. Remote/different-server attach uses Hive's existing PTY
and outer-tmux restoration behavior; tmux selection remains shared state.

Agent previews and process arguments stay in memory, are not written to the
picker cache, and are never logged. SSH continues to use the user's agent,
configuration, and host-key verification. Hosts are configuration-allowlisted,
commands are parameterized/quoted, inputs are validated, and concurrency and
output are bounded (ASVS 1.2.5, 1.5.2, 2.2.1, 8.2.2, 13.2.4/13.2.6,
14.2.3-14.2.4, and 15.4.1-15.4.2).
