package picker

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lum1n/hive/internal/cache"
	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/discover"
	"github.com/lum1n/hive/internal/execx"
	"github.com/lum1n/hive/internal/prefix"
	"github.com/lum1n/hive/internal/sshx"
	"github.com/lum1n/hive/internal/workspace"
)

type Action int

const (
	ActionQuit Action = iota
	ActionAttach
	ActionCreate
	ActionKill
	ActionRename
)

type Choice struct {
	Action    Action
	Workspace workspace.ID
	NewName   string
	Host      config.Host
}

type Options struct {
	Config config.Config
	Store  *cache.Store
	SSH    sshx.Options
	Runner execx.Runner
	Last   workspace.ID
}

type row struct {
	host    config.Host
	session cache.Session
	empty   bool
}

func (r row) id() string {
	if r.empty {
		return r.host.ID + "/"
	}
	return r.host.ID + "/" + r.session.Name
}

type mode int

const (
	modeList mode = iota
	modeConfirm
	modeName
)

type model struct {
	opts     Options
	results  <-chan discover.Result
	snaps    map[string]cache.HostSnapshot
	filter   string
	cursor   int
	width    int
	height   int
	mode     mode
	prompt   textinput.Model
	pending  Action
	status   string
	choice   Choice
	quitting bool
	refreshN int
	refreshD int
}

type resultMsg discover.Result

type refreshDoneMsg struct{}

func Run(ctx context.Context, opts Options) (Choice, error) {
	snaps := make(map[string]cache.HostSnapshot, len(opts.Config.Hosts))
	if opts.Store != nil {
		for _, h := range opts.Config.Hosts {
			snaps[h.ID] = opts.Store.LoadHost(h.ID)
		}
	}
	pctx, cancel := context.WithCancel(ctx)
	ch := make(chan discover.Result, len(opts.Config.Hosts))
	go func() {
		discover.RefreshAll(pctx, opts.Runner, opts.SSH, opts.Config.Hosts, 8, ch)
		close(ch)
	}()

	ti := textinput.New()
	ti.Placeholder = "session name"
	ti.CharLimit = 64
	ti.Prompt = "  name: "

	m := model{
		opts:     opts,
		results:  ch,
		snaps:    snaps,
		prompt:   ti,
		refreshN: len(opts.Config.Hosts),
	}
	if !opts.Last.Empty() {
		m.selectID(opts.Last.Display())
	}

	// Discovery uses pctx so we can stop it after the picker exits. The TUI
	// uses the parent ctx so a successful Enter is tea.Quit, not a kill.
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	final, err := p.Run()
	cancel()
	return finish(final, err)
}

func finish(final tea.Model, err error) (Choice, error) {
	if final != nil {
		out := final.(model)
		if out.choice.Action != ActionQuit {
			return out.choice, nil
		}
		if err != nil {
			return Choice{Action: ActionQuit}, err
		}
		return out.choice, nil
	}
	if err != nil {
		return Choice{Action: ActionQuit}, err
	}
	return Choice{Action: ActionQuit}, nil
}

func (m model) Init() tea.Cmd {
	return waitResult(m.results)
}

func waitResult(ch <-chan discover.Result) tea.Cmd {
	return func() tea.Msg {
		r, ok := <-ch
		if !ok {
			return refreshDoneMsg{}
		}
		return resultMsg(r)
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case resultMsg:
		m.snaps[msg.Host] = msg.Snapshot
		if m.opts.Store != nil {
			_ = m.opts.Store.SaveHost(msg.Host, msg.Snapshot)
		}
		m.refreshD++
		m.keepCursor()
		return m, waitResult(m.results)
	case refreshDoneMsg:
		m.refreshN = 0
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeName {
		return m.handleName(msg)
	}
	if m.mode == modeConfirm {
		return m.handleConfirm(msg)
	}
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		if m.filter != "" && msg.Type == tea.KeyEsc {
			m.filter = ""
			m.keepCursor()
			return m, nil
		}
		m.quitting = true
		m.choice = Choice{Action: ActionQuit}
		return m, tea.Quit
	case tea.KeyUp, tea.KeyCtrlK:
		m.move(-1)
		return m, nil
	case tea.KeyDown, tea.KeyCtrlJ:
		m.move(1)
		return m, nil
	case tea.KeyEnter:
		return m.submitAttach()
	case tea.KeyCtrlN:
		return m.beginName(ActionCreate)
	case tea.KeyCtrlX:
		return m.beginKill()
	case tea.KeyCtrlR:
		return m.beginName(ActionRename)
	case tea.KeyBackspace:
		if m.filter != "" {
			r := []rune(m.filter)
			m.filter = string(r[:len(r)-1])
			m.keepCursor()
		}
		return m, nil
	case tea.KeyRunes:
		if msg.Alt {
			return m, nil
		}
		m.filter += string(msg.Runes)
		m.keepCursor()
		return m, nil
	}
	switch msg.String() {
	case "ctrl+n":
		return m.beginName(ActionCreate)
	case "ctrl+x":
		return m.beginKill()
	case "ctrl+r":
		return m.beginName(ActionRename)
	}
	return m, nil
}

func (m model) handleName(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = modeList
		m.status = ""
		return m, nil
	case tea.KeyEnter:
		name := strings.TrimSpace(m.prompt.Value())
		if name == "" {
			m.status = "name required"
			return m, nil
		}
		host, ok := m.selectedHost()
		if !ok {
			m.status = "no host"
			return m, nil
		}
		cur := m.selected()
		m.choice = Choice{Action: m.pending, Host: host, NewName: name}
		if cur != nil && !cur.empty {
			m.choice.Workspace = workspace.ID{Host: cur.host.ID, Session: cur.session.Name}
		} else {
			m.choice.Workspace = workspace.ID{Host: host.ID, Session: name}
		}
		if m.pending == ActionCreate {
			m.choice.Workspace = workspace.ID{Host: host.ID, Session: name}
		}
		m.quitting = true
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	return m, cmd
}

func (m model) handleConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		cur := m.selected()
		if cur == nil || cur.empty {
			m.mode = modeList
			return m, nil
		}
		m.choice = Choice{
			Action:    ActionKill,
			Host:      cur.host,
			Workspace: workspace.ID{Host: cur.host.ID, Session: cur.session.Name},
		}
		m.quitting = true
		return m, tea.Quit
	default:
		m.mode = modeList
		m.status = ""
		return m, nil
	}
}

func (m model) submitAttach() (tea.Model, tea.Cmd) {
	rows := m.rows()
	if len(rows) == 0 {
		host, ok := m.createHost()
		name := m.createName()
		if !ok || name == "" {
			m.status = "type host/session or a name, then enter"
			return m, nil
		}
		m.choice = Choice{
			Action:    ActionCreate,
			Host:      host,
			Workspace: workspace.ID{Host: host.ID, Session: name},
			NewName:   name,
		}
		m.quitting = true
		return m, tea.Quit
	}
	cur := rows[m.cursor]
	if cur.empty {
		m.status = "no session — ctrl-n to create"
		return m, nil
	}
	m.choice = Choice{
		Action:    ActionAttach,
		Host:      cur.host,
		Workspace: workspace.ID{Host: cur.host.ID, Session: cur.session.Name},
	}
	m.quitting = true
	return m, tea.Quit
}

func (m model) beginName(act Action) (tea.Model, tea.Cmd) {
	host, ok := m.selectedHost()
	if !ok {
		m.status = "no host"
		return m, nil
	}
	if act == ActionRename {
		cur := m.selected()
		if cur == nil || cur.empty {
			m.status = "select a session to rename"
			return m, nil
		}
		m.prompt.SetValue(cur.session.Name)
	} else {
		m.prompt.SetValue(m.createName())
	}
	m.pending = act
	m.mode = modeName
	m.prompt.Focus()
	m.status = host.Display()
	return m, nil
}

func (m model) beginKill() (tea.Model, tea.Cmd) {
	cur := m.selected()
	if cur == nil || cur.empty {
		m.status = "select a session to kill"
		return m, nil
	}
	m.mode = modeConfirm
	m.status = "kill " + cur.id() + "? y/N"
	return m, nil
}

func (m model) createHost() (config.Host, bool) {
	if host, session, ok := strings.Cut(m.filter, "/"); ok {
		if h, found := m.opts.Config.Host(strings.TrimSpace(host)); found && strings.TrimSpace(session) != "" {
			return h, true
		}
	}
	return m.selectedHost()
}

func (m model) createName() string {
	if _, session, ok := strings.Cut(m.filter, "/"); ok {
		return strings.TrimSpace(session)
	}
	return strings.TrimSpace(m.filter)
}

func (m model) selectedHost() (config.Host, bool) {
	if cur := m.selected(); cur != nil {
		return cur.host, true
	}
	if len(m.opts.Config.Hosts) == 0 {
		return config.Host{}, false
	}
	return m.opts.Config.Hosts[0], true
}

func (m *model) selected() *row {
	rows := m.rows()
	if len(rows) == 0 || m.cursor < 0 || m.cursor >= len(rows) {
		return nil
	}
	return &rows[m.cursor]
}

func (m *model) move(delta int) {
	rows := m.rows()
	if len(rows) == 0 {
		m.cursor = 0
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(rows) {
		m.cursor = len(rows) - 1
	}
}

func (m *model) keepCursor() {
	rows := m.rows()
	if m.cursor >= len(rows) {
		m.cursor = max(0, len(rows)-1)
	}
}

func (m *model) selectID(id string) {
	rows := m.rows()
	for i, r := range rows {
		if r.id() == id || (!r.empty && r.host.ID+"/"+r.session.Name == id) {
			m.cursor = i
			return
		}
	}
}

func (m model) rows() []row {
	var out []row
	for _, h := range m.opts.Config.Hosts {
		snap := m.snaps[h.ID]
		sessions := snap.Sessions
		if len(sessions) == 0 {
			r := row{host: h, empty: true}
			if match(m.filter, r.id()) || match(m.filter, h.Display()) {
				out = append(out, r)
			}
			continue
		}
		for _, s := range sessions {
			r := row{host: h, session: s}
			if match(m.filter, r.id()) || match(m.filter, h.Display()+"/"+s.Name) || match(m.filter, s.Name) {
				out = append(out, r)
			}
		}
	}
	return out
}

func match(filter, target string) bool {
	if filter == "" {
		return true
	}
	f := []rune(strings.ToLower(filter))
	i := 0
	for _, r := range strings.ToLower(target) {
		if i < len(f) && r == f[i] {
			i++
		}
	}
	return i == len(f)
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	rows := m.rows()
	meta := fmt.Sprintf("%d sessions", len(rows))
	if label := m.refreshLabel(); label != "" {
		meta += "  " + label
	}

	var body strings.Builder
	listHeight := max(1, m.height-6)
	start := 0
	if m.cursor >= listHeight {
		start = m.cursor - listHeight + 1
	}
	end := min(len(rows), start+listHeight)
	if len(rows) == 0 {
		body.WriteString(mutedStyle.Render("  no matches — enter creates if you typed a name"))
		body.WriteByte('\n')
	}
	for i := start; i < end; i++ {
		body.WriteString(m.renderRow(rows[i], i == m.cursor, width))
		body.WriteByte('\n')
	}

	status := m.status
	if m.mode == modeName {
		status = m.prompt.View()
	}
	help := mutedStyle.Render("  enter attach   ctrl-n new   ctrl-x kill   ctrl-r rename   " + prefix.Label(m.opts.Config.Prefix) + " detaches   ctrl-c quit")

	return frame(width, header("hive", meta), filterLine(m.filter, m.mode == modeList), "", body.String(), status, help)
}

func (m model) refreshLabel() string {
	if m.refreshN == 0 {
		return ""
	}
	return fmt.Sprintf("refresh %d/%d", m.refreshD, m.refreshN)
}

func (m model) renderRow(r row, selected bool, width int) string {
	host := seg{r.host.Display(), hostStyle(m.opts.Config.Hosts, r.host.ID)}
	if r.empty {
		hint := "(no sessions)"
		if err := m.snaps[r.host.ID].Error; err != "" {
			hint = "(" + err + ")"
		}
		return renderRow(selected, width,
			col{leftWidth(width), []seg{host, {"/", mutedStyle}, {hint, mutedStyle}}},
			col{20, nil},
			col{0, []seg{statusBadge(m.snaps[r.host.ID].Status)}})
	}
	windows := fmt.Sprintf("%d window", r.session.Windows)
	if r.session.Windows != 1 {
		windows += "s"
	}
	attached := seg{}
	if r.session.Attached {
		attached = seg{"attached", plainStyle}
	}
	return renderRow(selected, width,
		col{leftWidth(width), []seg{host, {"/", mutedStyle}, {r.session.Name, plainStyle}}},
		col{10, []seg{{windows, mutedStyle}}},
		col{8, []seg{attached}},
		col{0, []seg{statusBadge(m.snaps[r.host.ID].Status)}})
}

func statusBadge(st cache.Status) seg {
	switch st {
	case cache.StatusOnline:
		return seg{"online", onlineStyle}
	case cache.StatusAuth:
		return seg{"auth", authStyle}
	case cache.StatusOffline:
		return seg{"offline", offlineStyle}
	default:
		return seg{"…", mutedStyle}
	}
}
