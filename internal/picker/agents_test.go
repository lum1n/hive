package picker

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lum1n/hive/internal/agents"
	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/execx"
)

func pickerAgent(host, socket, pane, kind string) agents.Agent {
	ref := agents.Reference{Version: 1, Host: host, Socket: socket, Generation: "1:1", Pane: pane}
	return agents.Agent{ID: ref.ID(), Host: host, Socket: socket, Pane: pane, Kind: kind,
		Path: "/synthetic/project", Members: []agents.Membership{{SessionName: "backend", WindowName: "work"}}}
}

func agentFixtureModel() agentModel {
	a := pickerAgent("local", "/one.sock", "%0", "copilot")
	b := pickerAgent("box", "/two.sock", "%0", "cursor")
	return agentModel{
		ctx: context.Background(), width: 80, height: 24,
		opts: AgentOptions{Client: agents.Client{Config: config.Config{Hosts: []config.Host{
			{ID: "local", Local: true}, {ID: "box", SSH: "box"},
		}}}},
		snaps: map[string]agents.HostResult{
			"local": {Host: "local", Status: "online", Servers: []agents.Server{{Socket: a.Socket, Status: "online", Agents: []agents.Agent{a}}}},
			"box":   {Host: "box", Status: "online", Servers: []agents.Server{{Socket: b.Socket, Status: "online", Agents: []agents.Agent{b}}}},
		},
	}
}

func agentKey(m agentModel, key tea.KeyMsg) (agentModel, tea.Cmd) {
	next, cmd := m.Update(key)
	return next.(agentModel), cmd
}

func TestAgentPickerFilterAndExactChoice(t *testing.T) {
	m := agentFixtureModel()
	for _, filter := range []string{"box/backend", "cursor", "/synthetic/project", "BOX/work"} {
		m.filter = filter
		if len(m.rows()) == 0 {
			t.Fatalf("agent filter failed: %s", filter)
		}
	}
	m.filter = "cursor"
	m, _ = agentKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	want := pickerAgent("box", "/two.sock", "%0", "cursor").ID
	if m.choice != want || !m.quitting {
		t.Fatal("picker lost the server-qualified agent reference")
	}
	killed := fmt.Errorf("%w: %w", tea.ErrProgramKilled, context.Canceled)
	if id, err := finishAgents(m, killed); err != nil || id != want {
		t.Fatal("successful selection was lost during TUI teardown")
	}
	if _, err := finishAgents(agentModel{}, killed); !errors.Is(err, tea.ErrProgramKilled) {
		t.Fatal("picker swallowed a terminal error")
	}
}

func TestAgentPickerEmptyFailureAndSafeKeys(t *testing.T) {
	m := agentFixtureModel()
	m.snaps["local"] = agents.HostResult{Host: "local", Status: "online"}
	m.snaps["box"] = agents.HostResult{Host: "box", Status: "offline",
		Error: &agents.Failure{Code: "offline", Message: "SSH host unavailable"}}
	if view := m.View(); !strings.Contains(view, "no agents") || !strings.Contains(view, "offline") {
		t.Fatal("failed host was rendered as a healthy empty inventory")
	}
	for _, key := range []tea.KeyType{tea.KeyEnter, tea.KeyCtrlN, tea.KeyCtrlX} {
		m, _ = agentKey(m, tea.KeyMsg{Type: key})
		if m.choice != "" || m.quitting {
			t.Fatal("empty/error agent row invoked a session action")
		}
	}
	m.filter = "no-match"
	m, _ = agentKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.choice != "" {
		t.Fatal("no matches created or attached to a target")
	}
	m, _ = agentKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.filter != "" || m.quitting {
		t.Fatal("escape did not clear the filter first")
	}
	m, _ = agentKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if !m.quitting {
		t.Fatal("escape did not quit")
	}
}

func TestAgentPickerStableRefreshAndLastSelection(t *testing.T) {
	m := agentFixtureModel()
	m.cursor = 1
	b := m.snaps["box"]
	a := pickerAgent("local", "/one.sock", "%1", "claude")
	local := m.snaps["local"]
	local.Servers = append([]agents.Server(nil), local.Servers...)
	local.Servers[0].Agents = append([]agents.Agent{a}, local.Servers[0].Agents...)
	next, _ := m.Update(agentResultMsg{host: "local", result: local})
	m = next.(agentModel)
	if m.selectedID() != b.Servers[0].Agents[0].ID {
		t.Fatal("asynchronous inventory update changed selected agent")
	}
	last := b.Servers[0].Agents[0].ID
	m.snaps = map[string]agents.HostResult{}
	m.opts.Last, m.restore, m.cursor = last, true, 0
	next, _ = m.Update(agentResultMsg{host: "local", result: local})
	m = next.(agentModel)
	next, _ = m.Update(agentResultMsg{host: "box", result: b})
	m = next.(agentModel)
	if m.selectedID() != last {
		t.Fatal("slow host prevented restoring the last exact agent")
	}
}

func TestAgentPickerSafeBoundedRendering(t *testing.T) {
	m := agentFixtureModel()
	local := m.snaps["local"]
	local.Servers[0].Agents[0].Path = "/synthetic/\x1b]52;c;unsafe\x07\u754c"
	m.snaps["local"] = local
	m.status = "\x1b]0;unsafe\x07status\nnext"
	m, _ = agentKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("x", 1000))})
	if len([]rune(m.filter)) != 256 {
		t.Fatal("filter exceeded its input limit")
	}
	m.filter = ""
	for _, size := range [][2]int{{80, 24}, {32, 8}, {12, 4}} {
		m.width, m.height = size[0], size[1]
		view := m.View()
		if strings.Contains(view, "\x1b]") || len(strings.Split(view, "\n")) > m.height {
			t.Fatal("unsafe controls or excessive rows reached the picker")
		}
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > m.width {
				t.Fatal("agent picker overflowed the terminal width")
			}
		}
	}
}

func emptyAgentProtocol() []byte {
	return []byte("V\t1\nP\t" + base64.StdEncoding.EncodeToString([]byte("1 0 sh")) + "\n")
}

func TestAgentRefreshBoundedAndCancellable(t *testing.T) {
	var active, peak atomic.Int32
	client := agents.Client{Runner: func(ctx context.Context, name string, args ...string) execx.Result {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		select {
		case <-ctx.Done():
			return execx.Result{Err: ctx.Err()}
		case <-time.After(10 * time.Millisecond):
			return execx.Result{Stdout: emptyAgentProtocol()}
		}
	}}
	for i := range 12 {
		client.Config.Hosts = append(client.Config.Hosts, config.Host{ID: fmt.Sprintf("fixture-%d", i)})
	}
	ctx, cancel := context.WithCancel(context.Background())
	results := refreshAgents(ctx, client)
	count := 0
	for result := range results {
		if result.err != nil || result.result.Status != "online" {
			t.Fatal("bounded synthetic refresh failed")
		}
		count++
	}
	if count != 12 || peak.Load() > 4 {
		t.Fatal("agent refresh lost hosts or exceeded concurrency")
	}
	cancel()
	results = refreshAgents(ctx, client)
	done := make(chan struct{})
	go func() {
		for range results {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled discovery did not stop")
	}
}

func TestAgentRefreshFastHostBeforeSlowHost(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := agents.Client{Config: config.Config{Hosts: []config.Host{{ID: "slow"}, {ID: "fast"}}},
		Runner: func(ctx context.Context, name string, args ...string) execx.Result {
			if strings.Contains(strings.Join(args, " "), " slow -- ") {
				<-ctx.Done()
				return execx.Result{Err: ctx.Err()}
			}
			return execx.Result{Stdout: emptyAgentProtocol()}
		}}
	results := refreshAgents(ctx, client)
	select {
	case result := <-results:
		if result.host != "fast" {
			t.Fatal("slow host prevented incremental discovery")
		}
	case <-time.After(time.Second):
		t.Fatal("fast host was not rendered promptly")
	}
	cancel()
	for range results {
	}
}
