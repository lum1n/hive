package agents

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/execx"
)

func syntheticWatcher(t *testing.T, host config.Host, payload, mode string) string {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("optional watcher integration needs Python 3")
	}
	path := filepath.Join(filepath.Dir(host.Socket), "w")
	script := `
import os, socket, sys, time
os.umask(0 if sys.argv[3] == "unsafe" else 0o077)
if sys.argv[3] == "unsafe-parent":
    os.umask(0)
    os.mkdir(os.path.dirname(sys.argv[1]), 0o777)
    os.umask(0o077)
listener = socket.socket(socket.AF_UNIX)
listener.bind(sys.argv[1])
listener.listen(8)
while True:
    connection, _ = listener.accept()
    with connection:
        if sys.argv[3] == "stall":
            time.sleep(2)
            continue
        try:
            if sys.argv[3] == "oversized":
                connection.sendall(b"x" * (1024 * 1024 + 1))
            else:
                connection.sendall((sys.argv[2] + "\n").encode())
        except BrokenPipeError:
            pass
`
	if mode == "unsafe-parent" {
		parent := filepath.Join(filepath.Dir(host.Socket), "unsafe")
		path = filepath.Join(parent, "w")
		t.Cleanup(func() {
			if err := os.Remove(parent); err != nil && !os.IsNotExist(err) {
				t.Error(err)
			}
		})
	}
	command := exec.Command(python, "-c", script, path, payload, mode)
	if err := command.Start(); err != nil {
		t.Fatal("synthetic watcher failed to start")
	}
	t.Cleanup(func() {
		command.Process.Kill()
		command.Wait()
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
	})
	for range 100 {
		if _, err := os.Lstat(path); err == nil {
			return path
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("synthetic watcher did not bind")
	return ""
}

func watcherPayload(kind, state string, unbound bool) string {
	raw, _ := json.Marshal(map[string]any{
		"v": 1, "type": "snapshot", "agents": []map[string]any{{
			"session": "fixture", "window": 0, "kind": kind, "state": state, "unbound": unbound,
		}},
	})
	return string(raw)
}

func nativeAgent(t *testing.T, client Client) Agent {
	t.Helper()
	listing, err := client.List(context.Background(), "")
	if err != nil || listing.Hosts[0].Error != nil || len(listing.Hosts[0].Servers) != 1 ||
		len(listing.Hosts[0].Servers[0].Agents) != 1 {
		t.Fatal("synthetic watcher inventory failed")
	}
	return listing.Hosts[0].Servers[0].Agents[0]
}

func nativeCapture(t *testing.T, client Client, agent Agent) Capture {
	t.Helper()
	result, err := client.Capture(context.Background(), []string{agent.ID}, 200)
	if err != nil || result.Captures[0].Error != nil {
		t.Fatal("optional watcher broke pane capture")
	}
	return result.Captures[0]
}

func TestNativeOptionalWatcherStatesAndDisable(t *testing.T) {
	host, pane := nativeFixture(t)
	path := syntheticWatcher(t, host, watcherPayload("copilot", "waiting-permission", false), "")
	nativeRun(t, host, "set-option", "-g", "@agent_watcher_socket", path)
	nativeRun(t, host, "send-keys", "-t", pane, "-l", ">")
	nativeRun(t, host, "send-keys", "-t", pane, "Enter")
	client := Client{Config: config.Config{Hosts: []config.Host{host}}, Runner: syntheticProcesses}
	agent := nativeAgent(t, client)
	if agent.State != "waiting-permission" || agent.Provenance != "shared" || agent.StateError != nil {
		t.Fatalf("watcher state not used in inventory: %s/%s", agent.State, agent.Provenance)
	}
	capture := nativeCapture(t, client, agent)
	if capture.State != "waiting-permission" || capture.Provenance != "shared" || capture.StateError != nil {
		t.Fatalf("watcher state not used in capture: %s/%s", capture.State, capture.Provenance)
	}
	nativeRun(t, host, "rename-session", "-t", "fixture", "renamed")
	if capture := nativeCapture(t, client, agent); capture.Provenance != "heuristic" {
		t.Fatal("obsolete watcher membership was used after rename")
	}
	client.Config.AgentWatcher = "off"
	agent = nativeAgent(t, client)
	if agent.State != "unknown" || agent.Provenance != "unavailable" || agent.StateError != nil {
		t.Fatal("disabled watcher was queried")
	}
	if capture := nativeCapture(t, client, agent); capture.Provenance != "heuristic" || capture.StateError != nil {
		t.Fatal("disabled watcher did not preserve standalone captures")
	}
}

func TestNativeWatcherSkipsFramesAndReportsQuota(t *testing.T) {
	host, _ := nativeFixture(t)
	frames := []string{
		`{"v":1,"type":"hello","role":"agent-watcher","via":"socket"}`,
		`{"v":1,"type":"quota","kind":"claude","plan":"Max 5x","stale":false,"windows":[` +
			`{"id":"session","label":"5h","usedPercent":33,"resetsAt":"2026-10-07T12:00:00Z"},` +
			`{"id":"week","label":"7d","usedPercent":13.5}]}`,
		`{"v":1,"type":"quota","kind":"codex","windows":[{"label":"5h","usedPercent":"bad"}]}`,
		`{"v":1,"type":"state","session":"other","window":3,"kind":"claude","state":"thinking"}`,
		`{"v":1,"type":"future-event"}`,
		watcherPayload("copilot", "idle", false),
	}
	path := syntheticWatcher(t, host, strings.Join(frames, "\n"), "")
	nativeRun(t, host, "set-option", "-g", "@agent_watcher_socket", path)
	client := Client{Config: config.Config{Hosts: []config.Host{host}}, Runner: syntheticProcesses}
	listing, err := client.List(context.Background(), "")
	if err != nil || len(listing.Hosts[0].Servers) != 1 || len(listing.Hosts[0].Servers[0].Agents) != 1 {
		t.Fatal("synthetic watcher inventory failed")
	}
	server := listing.Hosts[0].Servers[0]
	if agent := server.Agents[0]; agent.State != "idle" || agent.Provenance != "shared" || agent.StateError != nil {
		t.Fatalf("frames before the snapshot broke watcher state: %s/%s %v", agent.State, agent.Provenance, agent.StateError)
	}
	want := []Quota{{Kind: "claude", Plan: "Max 5x", Windows: []QuotaWindow{
		{Label: "5h", UsedPercent: 33, ResetsAt: "2026-10-07T12:00:00Z"}, {Label: "7d", UsedPercent: 13.5}}}}
	got, _ := json.Marshal(server.Quota)
	expected, _ := json.Marshal(want)
	if string(got) != string(expected) {
		t.Fatalf("server quota = %s, want %s", got, expected)
	}
	client.Config.AgentWatcher = "off"
	if listing, _ := client.List(context.Background(), ""); listing.Hosts[0].Servers[0].Quota != nil {
		t.Fatal("disabled watcher still reported quota")
	}
}

func TestNativeOptionalWatcherUnavailableFallback(t *testing.T) {
	for _, fixture := range []struct{ mode, payload, code string }{
		{"", `{"v":2,"type":"snapshot","agents":[]}`, "protocol"},
		{"", `not-json`, "protocol"},
		{"", watcherPayload("shell", "idle", false), "protocol"},
		{"unsafe", watcherPayload("copilot", "idle", false), "unavailable"},
		{"unsafe-parent", watcherPayload("copilot", "idle", false), "unavailable"},
		{"stall", "", "timeout"},
		{"oversized", "", "output_limit"},
	} {
		t.Run(fixture.mode+fixture.code, func(t *testing.T) {
			host, _ := nativeFixture(t)
			path := syntheticWatcher(t, host, fixture.payload, fixture.mode)
			nativeRun(t, host, "set-option", "-g", "@agent_watcher_socket", path)
			client := Client{Config: config.Config{Hosts: []config.Host{host}}, Runner: syntheticProcesses}
			started := time.Now()
			agent := nativeAgent(t, client)
			if agent.StateError == nil || agent.StateError.Code != fixture.code {
				t.Fatal("watcher failure not exposed separately from inventory success")
			}
			if time.Since(started) > 2*time.Second {
				t.Fatal("optional watcher delayed inventory excessively")
			}
			capture := nativeCapture(t, client, agent)
			if capture.Provenance != "heuristic" || capture.StateError == nil || capture.StateError.Code != fixture.code {
				t.Fatal("watcher failure did not fall back to capture heuristics")
			}
		})
	}
}

func TestNativeOptionalWatcherUnboundKindAndSplitGuards(t *testing.T) {
	for _, fixture := range []struct {
		kind    string
		unbound bool
		split   bool
	}{{"claude", false, false}, {"copilot", true, false}, {"copilot", false, true}} {
		t.Run(fixture.kind, func(t *testing.T) {
			host, pane := nativeFixture(t)
			path := syntheticWatcher(t, host, watcherPayload(fixture.kind, "thinking", fixture.unbound), "")
			nativeRun(t, host, "set-option", "-g", "@agent_watcher_socket", path)
			if fixture.split {
				nativeRun(t, host, "split-window", "-d", "-h", "-t", pane, "cat")
			}
			client := Client{Config: config.Config{Hosts: []config.Host{host}}, Runner: syntheticProcesses}
			agent := nativeAgent(t, client)
			if agent.Provenance != "unavailable" || agent.StateError != nil {
				t.Fatal("ambiguous watcher state applied to inventory")
			}
			if capture := nativeCapture(t, client, agent); capture.Provenance != "heuristic" || capture.StateError != nil {
				t.Fatal("ambiguous watcher state applied to capture")
			}
		})
	}
}

func TestWatcherProtocolValidationAndCaptureIdentity(t *testing.T) {
	for _, payload := range []string{
		`{"status":"invalid","states":[]}`,
		`{"status":"ready","states":[{"pane":"%1","window":"@1","pid":1,"kind":"copilot","state":"invalid"}]}`,
		`{"status":"ready","states":[{"pane":"%1","window":"@1","pid":1,"kind":"copilot","state":"idle"},{"pane":"%1","window":"@1","pid":1,"kind":"copilot","state":"idle"}]}`,
	} {
		if _, err := parseWatcher([]string{"W", b64(payload)}); err == nil {
			t.Fatal("malformed watcher protocol accepted")
		}
	}
	ref := testReference("local", "/fixture.sock")
	watcher := `{"status":"ready","states":[{"pane":"%1","window":"@1","pid":1,"kind":"copilot","state":"thinking"}]}`
	watchers, err := parseWatchers([]byte("V\t1\nB\t"+b64(ref.Socket)+"\nW\t"+b64(watcher)+"\n"), []Reference{ref})
	if err != nil {
		t.Fatal(err)
	}
	prefix := "V\t1\nP\t" + b64("1 0 sh") + "\n"
	for _, window := range []string{"@1", "@2"} {
		raw := prefix + "C\t0\t0\t1\t" + b64("copilot") + "\t" + b64("cat") + "\t" + b64(">") + "\t" + window + "\n"
		captures, err := parseCaptures([]byte(raw), []Reference{ref})
		if err != nil {
			t.Fatal(err)
		}
		capture := captures[0]
		_, matched := watchers[ref.Socket].state(ref.Pane, capture.window, capture.pid, capture.kind)
		if matched != (window == "@1") {
			t.Fatal("capture used watcher state for a different window")
		}
	}
	if strings.Contains(inventoryScript(config.Host{ID: "local"}), "python3") ||
		strings.Contains(captureScript(config.Host{ID: "local"}, []Reference{ref}, 200), "python3") {
		t.Fatal("core discovery/capture requires optional Python helper")
	}
}

func TestOptionalWatcherBudgetDoesNotFailInventory(t *testing.T) {
	client := Client{Config: config.Config{Hosts: []config.Host{{ID: "local", Local: true}}}, Timeout: 100 * time.Millisecond,
		Runner: func(ctx context.Context, name string, args ...string) execx.Result {
			if strings.Contains(strings.Join(args, " "), "python3 -c") {
				<-ctx.Done()
				return execx.Result{Err: ctx.Err()}
			}
			return execx.Result{Stdout: []byte(inventoryFixture())}
		}}
	started := time.Now()
	result, err := client.List(context.Background(), "")
	if err != nil || result.Hosts[0].Status != "online" || result.Hosts[0].Error != nil {
		t.Fatal("stalled optional state lookup failed successful inventory")
	}
	agent := result.Hosts[0].Servers[0].Agents[0]
	if agent.StateError == nil || agent.StateError.Code != "timeout" || time.Since(started) > time.Second {
		t.Fatal("optional lookup did not preserve the per-host deadline")
	}
}

func TestNativeOptionalWatcherWithoutPython(t *testing.T) {
	host, _ := nativeFixture(t)
	path := syntheticWatcher(t, host, watcherPayload("copilot", "idle", false), "")
	nativeRun(t, host, "set-option", "-g", "@agent_watcher_socket", path)
	client := Client{Config: config.Config{Hosts: []config.Host{host}},
		Runner: func(ctx context.Context, name string, args ...string) execx.Result {
			args = append([]string(nil), args...)
			args[1] = strings.ReplaceAll(args[1], "elif command -v python3 >/dev/null 2>&1; then", "elif false; then")
			return syntheticProcesses(ctx, name, args...)
		}}
	agent := nativeAgent(t, client)
	capture := nativeCapture(t, client, agent)
	if capture.Provenance != "heuristic" || capture.StateError == nil || capture.StateError.Code != "unavailable" {
		t.Fatal("missing optional Python runtime prevented standalone operation")
	}
}

func TestNativeCompanionWatcherWithoutTmuxAgentState(t *testing.T) {
	host, _ := nativeFixture(t)
	companion := host.Socket + ".agent-watcher.sock"
	if len(companion) >= 104 {
		t.Skip("platform temporary directory is too long for a companion Unix socket")
	}
	path := syntheticWatcher(t, host, watcherPayload("copilot", "thinking", false), "")
	if err := os.Rename(path, companion); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(companion); err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
	})
	client := Client{Config: config.Config{Hosts: []config.Host{host}}, Runner: syntheticProcesses}
	agent := nativeAgent(t, client)
	if agent.State != "thinking" || agent.Provenance != "shared" {
		t.Fatal("standalone companion watcher was not discovered")
	}
}

func TestNativeConflictingLinkedWatcherStates(t *testing.T) {
	host, pane := nativeFixture(t)
	nativeRun(t, host, "new-session", "-d", "-s", "linked", "cat")
	nativeRun(t, host, "link-window", "-s", "fixture:0", "-t", "linked:1")
	payload := `{"v":1,"type":"snapshot","agents":[` +
		`{"session":"fixture","window":0,"kind":"copilot","state":"thinking"},` +
		`{"session":"linked","window":1,"kind":"copilot","state":"errored"}]}`
	path := syntheticWatcher(t, host, payload, "")
	nativeRun(t, host, "set-option", "-g", "@agent_watcher_socket", path)
	client := Client{Config: config.Config{Hosts: []config.Host{host}}, Runner: syntheticProcesses}
	agent := nativeAgent(t, client)
	if agent.Pane != pane || agent.Provenance != "unavailable" {
		t.Fatal("conflicting linked watcher states attributed to a pane")
	}
	if capture := nativeCapture(t, client, agent); capture.Provenance != "heuristic" {
		t.Fatal("conflicting linked watcher states attributed to capture")
	}
}
