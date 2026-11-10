// Package tui is the interactive full-screen vteam view that `vteam` opens when run without arguments.
package tui

import (
	"context"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/schedule"
)

const (
	StatusRefreshInterval = 2 * time.Second
	AgentsRefreshInterval = 500 * time.Millisecond
	chromeRows            = 4
)

type Tab int

const (
	TabAgents Tab = iota
	TabOverview
	TabInfo
	TabAbout
)

var tabNames = []string{"Agents", "Overview", "Info", "About"}

type Project struct {
	Name string
	// FromAven is false when vteam.config.json could not be loaded and Name is the directory name.
	FromAven bool
	Label    string
	Branch   string
	Dir      string
}

type Deps struct {
	Project Project
	// RenderStatus renders the Overview: vteam status plus the pipeline sections over the last window.
	RenderStatus func(ctx context.Context, window time.Duration) ([]string, error)
	Agents       func(now time.Time) []Agent
	LoadConfig   func() (*config.Config, error)
	// SchedulerPID is the pid of a live `vteam cron start`, or 0.
	SchedulerPID func() int
	Now          func() time.Time

	// RunOnce starts one run of an agent (or the merge ensurer) in the background with the given `vteam run` flags.
	RunOnce func(name string, opts schedule.RunOptions) error
	// TaskChoices lists the tasks the run dialog offers an input: task agent, in pick order.
	TaskChoices func(ctx context.Context, agent string) ([]TaskChoice, error)
	// StopRun asks the live run of an agent (or the merge ensurer) to stop; keepWorktree leaves its worktree in place.
	StopRun func(name string, keepWorktree bool) error
	// EnableCron turns on a cron with expr; DisableCron turns it off.
	EnableCron  func(name, expr string) error
	DisableCron func(name string) error
	// SaveCron writes expr as the default pattern in agent.json (vteam.config.json for the merge ensurer).
	SaveCron func(name, expr string) error
	// StartScheduler starts `vteam cron start` in the background and returns its pid.
	StartScheduler func() (int, error)
}

type overviewState struct {
	window time.Duration
	// gen counts restarts of the refresh loop (refresh, window switch); results and ticks of an older loop are dropped.
	gen     int
	lines   []string
	err     string
	updated time.Time
	loaded  bool
	view    viewport.Model
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
	about    viewport.Model
	helpView viewport.Model
	dialog   dialog
	toast    toast
}

func New(ctx context.Context, deps Deps) Model {
	m := Model{
		ctx: ctx, deps: deps, width: 80, height: 24, agents: newAgentsState(),
		overview: overviewState{window: pipelineWindows[0], view: newScrollView()}, info: infoState{view: newScrollView()}, about: newScrollView(),
		helpView: newScrollView(),
	}
	m.agents = m.agents.reload(deps.Agents(deps.Now()))
	m.info = m.refreshInfo()
	return m
}

func Run(ctx context.Context, deps Deps) error {
	_, err := tea.NewProgram(New(ctx, deps), tea.WithContext(ctx)).Run()
	return err
}

var pipelineWindows = []time.Duration{7 * 24 * time.Hour, 24 * time.Hour}

func nextWindow(w time.Duration) time.Duration {
	i := slices.Index(pipelineWindows, w)
	return pipelineWindows[(i+1)%len(pipelineWindows)]
}

type statusMsg struct {
	gen     int
	lines   []string
	err     error
	started time.Time
}

type statusTickMsg struct{ gen int }

type agentsTickMsg struct{}

func agentsTick() tea.Cmd {
	return tea.Tick(AgentsRefreshInterval, func(time.Time) tea.Msg { return agentsTickMsg{} })
}

func (m Model) fetchStatus() tea.Msg {
	started := m.deps.Now()
	lines, err := m.deps.RenderStatus(m.ctx, m.overview.window)
	return statusMsg{gen: m.overview.gen, lines: lines, err: err, started: started}
}

// restartStatus fetches the Overview now and starts a new refresh loop, retiring the old one.
func (m Model) restartStatus() (Model, tea.Cmd) {
	m.overview.gen++
	return m, m.fetchStatus
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchStatus, agentsTick())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		next, cmd := m.handleKey(msg)
		return next.(Model).requestTaskChoices(cmd)
	case taskChoicesMsg:
		return m.receiveTaskChoices(msg), nil
	case tea.MouseWheelMsg:
		return m.handleWheel(msg), nil
	case tea.PasteMsg:
		var cmd tea.Cmd
		switch m.dialog.kind {
		case dialogRun:
			m.dialog.focus, cmd = m.dialog.focus.Update(msg)
		case dialogCron:
			m.dialog.input, cmd = m.dialog.input.Update(msg)
		}
		return m, cmd
	case statusMsg:
		if msg.gen != m.overview.gen {
			return m, nil
		}
		m.overview.loaded = true
		m.overview.updated = m.deps.Now()
		m.overview.err = ""
		if msg.err != nil {
			m.overview.err = firstLine(msg.err.Error())
		} else {
			m.overview.lines = dropLeadingBlank(splitLines(msg.lines[min(1, len(msg.lines)):]))
		}
		wait := max(0, StatusRefreshInterval-m.deps.Now().Sub(msg.started))
		gen := m.overview.gen
		return m, tea.Tick(wait, func(time.Time) tea.Msg { return statusTickMsg{gen: gen} })
	case statusTickMsg:
		if msg.gen != m.overview.gen {
			return m, nil
		}
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
	m = m.syncViewport()
	body := m.renderBody()
	if m.tab == TabAgents && m.dialog.kind != dialogNone {
		body = m.renderDialog(body)
	}
	if m.help {
		body = overlay(body, m.helpBox(), m.width, m.bodyHeight())
	}
	lines := append(m.renderHeader(), body...)
	lines = append(lines, m.renderFooter()...)
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m Model) renderBody() []string {
	switch m.tab {
	case TabAgents:
		return m.renderAgents()
	case TabInfo:
		return strings.Split(m.info.view.View(), "\n")
	case TabAbout:
		return strings.Split(m.about.View(), "\n")
	default:
		return strings.Split(m.overview.view.View(), "\n")
	}
}

// syncViewport sizes the active tab's scrollable pane to the window and refills it, so scrolling and
// rendering both see the current content. A following log pane stays at the bottom.
func (m Model) syncViewport() Model {
	if m.help {
		m = m.syncHelp()
	}
	switch m.tab {
	case TabAgents:
		fill(&m.agents.view, m.logRows(), m.logPaneWidth(), m.logBodyHeight())
		if m.agents.follow {
			m.agents.view.GotoBottom()
		}
	case TabInfo:
		fill(&m.info.view, stretchSections(m.infoLines(), m.width-1), m.width, m.bodyHeight())
	case TabAbout:
		fill(&m.about, stretchSections(m.aboutLines(), m.width-1), m.width, m.bodyHeight())
	default:
		lines := stretchSections(m.overview.lines, m.width-1)
		if !m.overview.loaded {
			lines = []string{styleMuted.Render("  loading status from aven…")}
		}
		fill(&m.overview.view, lines, m.width, m.bodyHeight())
	}
	return m
}

// fill resizes vp and sets its lines, each cut to the width inside vp's frame so long lines end in "…".
func fill(vp *viewport.Model, lines []string, width, height int) {
	vp.SetWidth(width)
	vp.SetHeight(height)
	inner := width - vp.Style.GetHorizontalFrameSize()
	fitted := make([]string, len(lines))
	for i, line := range lines {
		fitted[i] = fit(line, inner)
	}
	vp.SetContentLines(fitted)
}

func firstLine(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	return first
}
