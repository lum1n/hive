package agents

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/sshx"
	"github.com/lum1n/hive/internal/tmux"
)

//go:embed watcher_probe.py
var watcherPython string

func watcherBatchScript(host config.Host, refs []Reference) string {
	var b strings.Builder
	b.WriteString(tmux.Prelude(host.TmuxBin(), host.Socket) + probeHelpers + "\nprintf 'V\\t1\\n'\n")
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref.Socket] {
			continue
		}
		seen[ref.Socket] = true
		b.WriteString("printf 'B\\t" + base64.StdEncoding.EncodeToString([]byte(ref.Socket)) + "\\n'\n")
		b.WriteString("(\n" + targetGuard(ref) + watcherScript() + ") || printf 'W\\t" + b64WatcherUnavailable + "\\n'\n")
	}
	return b.String()
}

func (c Client) watchers(ctx context.Context, host config.Host, refs []Reference) map[string]watcherSnapshot {
	result := map[string]watcherSnapshot{}
	if c.Config.AgentWatcher == "off" || len(refs) == 0 {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer cancel()
	raw := c.run(ctx, host, watcherBatchScript(host, refs))
	var fail *Failure
	if raw.Err != nil {
		fail = commandFailure(raw)
	} else {
		var err error
		result, err = parseWatchers(raw.Stdout, refs)
		if err == nil {
			return result
		}
		fail = failure("protocol", "optional agent-watcher response invalid")
	}
	result = map[string]watcherSnapshot{}
	for _, ref := range refs {
		code := fail.Code
		switch code {
		case "timeout", "protocol", "output_limit", "stale", "cancelled":
		default:
			code = "unavailable"
		}
		result[ref.Socket] = watcherSnapshot{Status: "unavailable", Error: code}
	}
	return result
}

func parseWatchers(raw []byte, refs []Reference) (map[string]watcherSnapshot, error) {
	rows, err := protocol(raw)
	if err != nil {
		return nil, err
	}
	expected, result := map[string]bool{}, map[string]watcherSnapshot{}
	for _, ref := range refs {
		expected[ref.Socket] = true
	}
	server := ""
	for _, fields := range rows {
		if fields[0] == "B" && len(fields) == 2 && server == "" {
			server, err = decoded(fields[1], 1024)
			if err != nil || !expected[server] {
				return nil, fmt.Errorf("unexpected watcher server")
			}
			if _, duplicate := result[server]; duplicate {
				return nil, fmt.Errorf("duplicate watcher server")
			}
		} else if fields[0] == "W" && server != "" {
			watcher, err := parseWatcher(fields)
			if err != nil {
				return nil, err
			}
			result[server], server = watcher, ""
		} else {
			return nil, fmt.Errorf("invalid watcher batch")
		}
	}
	if server != "" || len(result) != len(expected) {
		return nil, fmt.Errorf("incomplete watcher batch")
	}
	return result, nil
}

type watcherState struct {
	Pane   string `json:"pane"`
	Window string `json:"window"`
	PID    int    `json:"pid"`
	Kind   string `json:"kind"`
	State  string `json:"state"`
}

type watcherSnapshot struct {
	Status string         `json:"status"`
	States []watcherState `json:"states"`
	Quota  []Quota        `json:"quota"`
	Error  string         `json:"error"`
}

// Quota is a server watcher's last subscription usage reading for one kind.
type Quota struct {
	Kind    string        `json:"kind"`
	Plan    string        `json:"plan,omitempty"`
	Stale   bool          `json:"stale"`
	Windows []QuotaWindow `json:"windows"`
}

type QuotaWindow struct {
	Label       string  `json:"label"`
	UsedPercent float64 `json:"used_percent"`
	ResetsAt    string  `json:"resets_at,omitempty"`
}

// usableQuota keeps well-formed readings with sanitized text. Quota is
// optional, so a bad reading is dropped rather than failing watcher states.
func usableQuota(quota []Quota) []Quota {
	var result []Quota
	seen := map[string]bool{}
	for _, q := range quota {
		if len(result) == len(Kinds) {
			break
		}
		plan := display(q.Plan)
		if !knownKind(q.Kind) || seen[q.Kind] || len(plan) > 64 || len(q.Windows) == 0 || len(q.Windows) > 8 {
			continue
		}
		windows := make([]QuotaWindow, 0, len(q.Windows))
		for _, w := range q.Windows {
			label := display(w.Label)
			if label == "" || len(label) > 32 || w.UsedPercent < 0 || w.UsedPercent > 100 {
				windows = nil
				break
			}
			if _, err := time.Parse(time.RFC3339, w.ResetsAt); w.ResetsAt != "" && err != nil {
				windows = nil
				break
			}
			windows = append(windows, QuotaWindow{Label: label, UsedPercent: w.UsedPercent, ResetsAt: w.ResetsAt})
		}
		if windows == nil {
			continue
		}
		seen[q.Kind] = true
		result = append(result, Quota{Kind: q.Kind, Plan: plan, Stale: q.Stale, Windows: windows})
	}
	return result
}

func watcherScript() string {
	return `
_watcher=$("$HIVE_TMUX_BIN" -u -S "$_sock" show-options -gqv @agent_watcher_socket 2>/dev/null)
_configured=0
[ -z "$_watcher" ] || _configured=1
[ -n "$_watcher" ] || _watcher="${_sock}.agent-watcher.sock"
if [ ! -e "$_watcher" ] && [ ! -L "$_watcher" ]; then
if [ "$_configured" -eq 1 ]; then
printf 'W\t` + b64WatcherUnavailable + `\n'
else
printf 'W\t` + b64WatcherAbsent + `\n'
fi
elif command -v python3 >/dev/null 2>&1; then
python3 -c ` + sshx.SingleQuote(watcherPython) + ` "$_watcher" "$HIVE_TMUX_BIN" "$_sock" "$_generation" 2>/dev/null || printf 'W\t` + b64WatcherUnavailable + `\n'
else
printf 'W\t` + b64WatcherUnavailable + `\n'
fi
`
}

var b64WatcherAbsent = base64.StdEncoding.EncodeToString([]byte(`{"status":"absent","states":[]}`))
var b64WatcherUnavailable = base64.StdEncoding.EncodeToString([]byte(`{"status":"unavailable","states":[],"error":"unavailable"}`))

func parseWatcher(fields []string) (watcherSnapshot, error) {
	var snapshot watcherSnapshot
	if len(fields) != 2 {
		return snapshot, fmt.Errorf("invalid watcher record")
	}
	raw, err := decoded(fields[1], 1024*1024)
	if err != nil || json.Unmarshal([]byte(raw), &snapshot) != nil || len(snapshot.States) > 4096 {
		return snapshot, fmt.Errorf("invalid watcher snapshot")
	}
	if snapshot.Status != "ready" {
		snapshot.Quota = nil
	}
	if snapshot.Status == "absent" && len(snapshot.States) == 0 && snapshot.Error == "" {
		return snapshot, nil
	}
	if snapshot.Status == "unavailable" && len(snapshot.States) == 0 {
		switch snapshot.Error {
		case "timeout", "unavailable", "protocol", "output_limit", "stale":
			return snapshot, nil
		}
	}
	if snapshot.Status != "ready" || snapshot.Error != "" {
		return snapshot, fmt.Errorf("invalid watcher status")
	}
	snapshot.Quota = usableQuota(snapshot.Quota)
	seen := map[string]bool{}
	for _, state := range snapshot.States {
		if !paneID.MatchString(state.Pane) || !windowID.MatchString(state.Window) ||
			state.PID < 1 || !knownKind(state.Kind) || !knownState(state.State) || seen[state.Pane] {
			return snapshot, fmt.Errorf("invalid watcher state")
		}
		seen[state.Pane] = true
	}
	return snapshot, nil
}

func knownState(state string) bool {
	switch state {
	case "unknown", "idle", "thinking", "running-tool", "waiting-permission", "errored":
		return true
	}
	return false
}

func (w watcherSnapshot) state(pane, window string, pid int, kind string) (string, bool) {
	for _, row := range w.States {
		if row.Pane == pane && (window == "" || row.Window == window) && row.PID == pid && row.Kind == kind {
			return row.State, true
		}
	}
	return "", false
}

func (w watcherSnapshot) failure() *Failure {
	if w.Status != "unavailable" {
		return nil
	}
	return failure(w.Error, "optional agent-watcher state unavailable")
}
