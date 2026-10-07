package picker

import (
	"context"
	"fmt"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/lum1n/hive/internal/agents"
	"github.com/lum1n/hive/internal/prefix"
)

type AgentOptions struct {
	Client agents.Client
	Last   string
	Status string
}

type agentResultMsg struct {
	host   string
	result agents.HostResult
	err    error
}

type agentRefreshDoneMsg struct{}

type agentRow struct {
	host   agents.HostResult
	agent  agents.Agent
	socket string
	hint   string
}

type agentModel struct {
	opts     AgentOptions
	ctx      context.Context
	results  <-chan agentResultMsg
	snaps    map[string]agents.HostResult
	filter   string
	cursor   int
	width    int
	height   int
	refreshD int
	busy     bool
	status   string
	choice   string
	quitting bool
	restore  bool
}

func RunAgents(ctx context.Context, opts AgentOptions) (string, error) {
	if len(opts.Client.Config.Hosts) > 64 {
		return "", fmt.Errorf("agent picker supports at most 64 hosts; select one with --host")
	}
	pctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := agentModel{
		opts: opts, ctx: pctx, snaps: make(map[string]agents.HostResult),
		status: opts.Status, busy: true, results: refreshAgents(pctx, opts.Client),
		restore: opts.Last != "",
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	final, err := p.Run()
	return finishAgents(final, err)
}

func finishAgents(final tea.Model, err error) (string, error) {
	if out, ok := final.(agentModel); ok {
		if out.choice != "" {
			return out.choice, nil
		}
	}
	return "", err
}

func refreshAgents(ctx context.Context, client agents.Client) <-chan agentResultMsg {
	results := make(chan agentResultMsg, len(client.Config.Hosts))
	go func() {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		for _, host := range client.Config.Hosts {
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					return
				}
				response, err := client.List(ctx, host.ID)
				result := agentResultMsg{host: host.ID, err: err}
				if err == nil {
					result.result = response.Hosts[0]
				}
				results <- result
			}()
		}
		wg.Wait()
		close(results)
	}()
	return results
}

func waitAgentResult(results <-chan agentResultMsg) tea.Cmd {
	return func() tea.Msg {
		result, ok := <-results
		if !ok {
			return agentRefreshDoneMsg{}
		}
		return result
	}
}

func (m agentModel) Init() tea.Cmd {
	return waitAgentResult(m.results)
}

func (m agentModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case agentResultMsg:
		selected := m.selectedID()
		if m.restore {
			selected = m.opts.Last
		}
		if msg.err != nil {
			m.snaps[msg.host] = agents.HostResult{Host: msg.host, Status: "unavailable",
				Error: &agents.Failure{Code: "discovery", Message: "agent inventory failed"}}
		} else {
			m.snaps[msg.host] = msg.result
		}
		m.refreshD++
		m.selectID(selected)
		return m, waitAgentResult(m.results)
	case agentRefreshDoneMsg:
		m.busy = false
		m.restore = false
	case tea.KeyMsg:
		if msg.Type != tea.KeyCtrlR {
			m.restore = false
		}
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			if msg.Type == tea.KeyEsc && m.filter != "" {
				m.filter = ""
				m.move(0)
				break
			}
			m.quitting = true
			return m, tea.Quit
		case tea.KeyUp, tea.KeyCtrlK:
			m.move(-1)
		case tea.KeyDown, tea.KeyCtrlJ:
			m.move(1)
		case tea.KeyPgUp:
			m.move(-max(1, m.height-7))
		case tea.KeyPgDown:
			m.move(max(1, m.height-7))
		case tea.KeyEnter:
			if m.height > 0 && m.height < 8 {
				break
			}
			rows := m.rows()
			if m.cursor < len(rows) && rows[m.cursor].agent.ID != "" {
				m.choice = rows[m.cursor].agent.ID
				m.quitting = true
				return m, tea.Quit
			}
			m.status = "select a running agent to attach"
		case tea.KeyCtrlR:
			if !m.busy {
				m.busy, m.refreshD = true, 0
				m.status = ""
				m.results = refreshAgents(m.ctx, m.opts.Client)
				return m, waitAgentResult(m.results)
			}
		case tea.KeyBackspace:
			runes := []rune(m.filter)
			if len(runes) > 0 {
				m.filter = string(runes[:len(runes)-1])
				m.move(0)
			}
		case tea.KeyRunes:
			if !msg.Alt {
				runes := []rune(m.filter + agentText(string(msg.Runes)))
				m.filter = string(runes[:min(len(runes), 256)])
				m.cursor = 0
			}
		}
	}
	return m, nil
}

func (m agentModel) rows() []agentRow {
	var rows []agentRow
	for _, host := range m.opts.Client.Config.Hosts {
		snap, ok := m.snaps[host.ID]
		snap.Host, snap.Label = host.ID, host.Display()
		add := func(row agentRow) {
			target := row.host.Host + " " + row.host.Label + " " + row.socket + " " +
				row.agent.Kind + " " + row.agent.Path + " " + row.agent.Pane
			for _, member := range row.agent.Members {
				target += " " + row.host.Host + "/" + member.SessionName + "/" + member.WindowName +
					" " + row.host.Label + "/" + member.SessionName + "/" + member.WindowName
			}
			if match(m.filter, agentText(target)) {
				rows = append(rows, row)
			}
		}
		if !ok {
			add(agentRow{host: snap, hint: "loading"})
			continue
		}
		if snap.Error != nil {
			add(agentRow{host: snap, hint: snap.Status + ": " + snap.Error.Message})
			continue
		}
		count := 0
		for _, server := range snap.Servers {
			if server.Error != nil {
				add(agentRow{host: snap, socket: server.Socket, hint: server.Status + ": " + server.Error.Message})
				count++
				continue
			}
			for _, agent := range server.Agents {
				add(agentRow{host: snap, socket: server.Socket, agent: agent})
				count++
			}
		}
		if count == 0 {
			add(agentRow{host: snap, hint: "no agents"})
		}
	}
	return rows
}

func (m agentModel) selectedID() string {
	rows := m.rows()
	if m.cursor < len(rows) {
		return rows[m.cursor].agent.ID
	}
	return ""
}

func (m *agentModel) selectID(id string) {
	if id != "" {
		for i, row := range m.rows() {
			if row.agent.ID == id {
				m.cursor = i
				return
			}
		}
	}
	m.move(0)
}

func (m *agentModel) move(delta int) {
	m.cursor = max(0, min(m.cursor+delta, len(m.rows())-1))
}

func agentText(text string) string {
	return strings.Join(strings.Fields(agents.Clean(text)), " ")
}

func (m agentModel) View() string {
	if m.quitting {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	if m.height > 0 && m.height < 8 {
		return ansi.Truncate("hive agents: terminal too small", width, "…")
	}
	rows := m.rows()
	count := 0
	for _, row := range rows {
		if row.agent.ID != "" {
			count++
		}
	}
	meta := fmt.Sprintf("%d agents", count)
	if m.busy {
		meta += fmt.Sprintf("  refresh %d/%d", m.refreshD, len(m.opts.Client.Config.Hosts))
	}
	var body strings.Builder
	height := max(1, m.height-7)
	start := max(0, m.cursor-height+1)
	for i := start; i < min(len(rows), start+height); i++ {
		body.WriteString(m.renderRow(rows[i], i == m.cursor, width))
		body.WriteByte('\n')
	}
	if len(rows) == 0 {
		body.WriteString(mutedStyle.Render("  no matching agents") + "\n")
	}
	help := mutedStyle.Render("  enter attach   ctrl-r refresh   " +
		prefix.Label(m.opts.Client.Config.Prefix) + " detaches   esc back   ctrl-c quit")
	return frame(width, header("hive agents", meta), filterLine(m.filter, true), "", body.String(), agentText(m.status), help)
}

func (m agentModel) renderRow(row agentRow, selected bool, width int) string {
	host := seg{agentText(row.host.Label), hostStyle(m.opts.Client.Config.Hosts, row.host.Host)}
	slash := seg{"/", mutedStyle}
	if row.agent.ID == "" {
		return renderRow(selected, width, col{0, []seg{host, slash, {"(" + agentText(row.hint) + ")", mutedStyle}}})
	}
	session, window := row.agent.Session, row.agent.Window
	if len(row.agent.Members) > 0 {
		session, window = row.agent.Members[0].SessionName, row.agent.Members[0].WindowName
	}
	return renderRow(selected, width,
		col{leftWidth(width), []seg{host, slash, {agentText(session), plainStyle}}},
		col{14, []seg{{agentText(window), mutedStyle}}},
		col{8, []seg{{agentText(row.agent.Kind), plainStyle}}},
		col{10, []seg{stateBadge(row.agent)}},
		col{0, []seg{{agentText(row.agent.Path), mutedStyle}}})
}

func stateBadge(agent agents.Agent) seg {
	if agent.StateError != nil {
		return seg{"no watcher", mutedStyle}
	}
	switch agent.State {
	case "idle":
		return seg{"idle", onlineStyle}
	case "thinking":
		return seg{"thinking", busyStyle}
	case "running-tool":
		return seg{"tool", busyStyle}
	case "waiting-permission":
		return seg{"permission", authStyle}
	case "errored":
		return seg{"error", offlineStyle}
	default:
		return seg{"…", mutedStyle}
	}
}
