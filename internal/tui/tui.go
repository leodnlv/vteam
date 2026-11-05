// Package tui is the interactive full-screen vteam view that `vteam` opens when run without arguments.
package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/leodnlv/vteam/internal/config"
)

const (
	StatusRefreshInterval = 2 * time.Second
	AgentsRefreshInterval = 500 * time.Millisecond
	chromeRows            = 4
)

type Tab int

const (
	TabOverview Tab = iota
	TabAgents
	TabInfo
)

var tabNames = []string{"Overview", "Agents", "Info"}

type Project struct {
	Name string
	// FromAven is false when vteam.config.json could not be loaded and Name is the directory name.
	FromAven bool
	Label    string
	Branch   string
	Dir      string
}

type Deps struct {
	Project      Project
	RenderStatus func(ctx context.Context) ([]string, error)
	Agents       func(now time.Time) []Agent
	LoadConfig   func() (*config.Config, error)
	// SchedulerPID is the pid of a live `vteam cron start`, or 0.
	SchedulerPID func() int
	Now          func() time.Time
}

type overviewState struct {
	lines   []string
	err     string
	updated time.Time
	loaded  bool
	offset  int
}

type Model struct {
	ctx      context.Context
	deps     Deps
	width    int
	height   int
	tab      Tab
	help     bool
	pendingG bool
	overview overviewState
	agents   agentsState
	info     infoState
}

func New(ctx context.Context, deps Deps) Model {
	m := Model{ctx: ctx, deps: deps, width: 80, height: 24, agents: newAgentsState()}
	m.agents = m.agents.reload(deps.Agents(deps.Now()))
	m.info = m.refreshInfo()
	return m
}

func Run(ctx context.Context, deps Deps) error {
	_, err := tea.NewProgram(New(ctx, deps), tea.WithContext(ctx)).Run()
	return err
}

type statusMsg struct {
	lines   []string
	err     error
	started time.Time
}

type statusTickMsg struct{}

type agentsTickMsg struct{}

func agentsTick() tea.Cmd {
	return tea.Tick(AgentsRefreshInterval, func(time.Time) tea.Msg { return agentsTickMsg{} })
}

func (m Model) fetchStatus() tea.Msg {
	started := m.deps.Now()
	lines, err := m.deps.RenderStatus(m.ctx)
	return statusMsg{lines: lines, err: err, started: started}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchStatus, agentsTick())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		return m.handleKey(msg.String())
	case statusMsg:
		m.overview.loaded = true
		m.overview.updated = m.deps.Now()
		m.overview.err = ""
		if msg.err != nil {
			m.overview.err = firstLine(msg.err.Error())
		} else {
			m.overview.lines = dropLeadingBlank(splitLines(msg.lines[min(1, len(msg.lines)):]))
		}
		wait := max(0, StatusRefreshInterval-m.deps.Now().Sub(msg.started))
		return m, tea.Tick(wait, func(time.Time) tea.Msg { return statusTickMsg{} })
	case statusTickMsg:
		return m, m.fetchStatus
	case agentsTickMsg:
		m.agents = m.agents.reload(m.deps.Agents(m.deps.Now()))
		m.info = m.refreshInfo()
		return m, agentsTick()
	}
	return m, nil
}

func (m Model) bodyHeight() int {
	return max(1, m.height-chromeRows)
}

func (m Model) View() tea.View {
	body := m.renderBody()
	if m.help {
		body = overlay(body, helpBox(), m.width, m.bodyHeight())
	}
	lines := append(m.renderHeader(), body...)
	lines = append(lines, m.renderFooter()...)
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	return v
}

func (m Model) renderBody() []string {
	height := m.bodyHeight()
	switch m.tab {
	case TabAgents:
		return m.renderAgents()
	case TabInfo:
		lines := m.infoLines()
		return window(lines, clampOffset(m.info.offset, len(lines), height), height, m.width)
	default:
		lines := m.overview.lines
		if !m.overview.loaded {
			lines = []string{styleGray.Render("  loading status from aven…")}
		}
		return window(lines, clampOffset(m.overview.offset, len(lines), height), height, m.width)
	}
}

func firstLine(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	return first
}
