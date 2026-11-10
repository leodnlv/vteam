package tui

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

// keyMap holds every key binding; key handling matches against it and the footer and help overlay render from it.
type keyMap struct {
	Tabs, Refresh, Help, Quit, ForceQuit key.Binding

	Down, Up, HalfPageDown, HalfPageUp, Top, GG, Bottom key.Binding

	SwitchPane, FocusLog, FocusSidebar, PrevRun, NextRun                key.Binding
	Actions, Run, StopRun, Cron, StopCron, StartScheduler, Follow, Wrap key.Binding

	Window key.Binding

	Confirm, Cancel, SaveDefault, Interactive, NewLine, TaskDown, TaskUp key.Binding
}

func binding(keys []string, help, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(help, desc))
}

var keys = keyMap{
	Tabs:      binding([]string{"1", "2", "3", "4"}, "1 2 3 4", "jump to Agents / Overview / Info / About"),
	Refresh:   binding([]string{"ctrl+r"}, "ctrl+r", "refresh now"),
	Help:      binding([]string{"?"}, "?", "toggle this help"),
	Quit:      binding([]string{"q"}, "q  ctrl+c", "quit"),
	ForceQuit: binding([]string{"ctrl+c"}, "ctrl+c", "quit"),

	Down:         binding([]string{"j", "down"}, "j  ↓", "line down"),
	Up:           binding([]string{"k", "up"}, "k  ↑", "line up"),
	HalfPageDown: binding([]string{"ctrl+d", "pgdown", "space"}, "ctrl+d", "half page down"),
	HalfPageUp:   binding([]string{"ctrl+u", "pgup"}, "ctrl+u", "half page up"),
	Top:          binding([]string{"home"}, "home", "top"),
	GG:           binding([]string{"g"}, "gg", "top"),
	Bottom:       binding([]string{"G", "end"}, "G", "bottom"),

	SwitchPane:     binding([]string{"tab", "shift+tab"}, "tab  shift+tab", "switch between the sidebar and the log"),
	FocusLog:       binding([]string{"l", "enter", "right"}, "l  enter  →", "focus the log"),
	FocusSidebar:   binding([]string{"h", "esc", "left"}, "h  esc  ←", "back to the sidebar"),
	PrevRun:        binding([]string{"p"}, "p", "previous (older) run"),
	NextRun:        binding([]string{"n"}, "n", "next (newer) run"),
	Actions:        binding([]string{"space", "a"}, "space  a", "actions menu of the selected agent"),
	Run:            binding([]string{"r"}, "r", "run the selected agent once"),
	StopRun:        binding([]string{"x"}, "x", "stop its run"),
	Cron:           binding([]string{"c"}, "c", "run it on cron / edit its cron"),
	StopCron:       binding([]string{"s"}, "s", "stop its cron"),
	StartScheduler: binding([]string{"S"}, "S", "start the scheduler when crons are paused"),
	Follow:         binding([]string{"f"}, "f", "toggle follow"),
	Wrap:           binding([]string{"w"}, "w", "toggle line wrap"),

	Window: binding([]string{"t"}, "t", "switch the pipeline window: 24h / 7d"),

	Confirm:     binding([]string{"enter"}, "⏎", "confirm"),
	Cancel:      binding([]string{"esc"}, "esc", "cancel"),
	SaveDefault: binding([]string{"tab"}, "tab", "save as default"),
	Interactive: binding([]string{"tab"}, "tab", "interactive session"),
	NewLine:     binding([]string{"shift+enter", "alt+enter", "ctrl+j"}, "shift+⏎/ctrl+j", "new line"),
	TaskDown:    binding([]string{"down", "ctrl+n"}, "↓", "next task"),
	TaskUp:      binding([]string{"up", "ctrl+p"}, "↑", "previous task"),
}

// relabel returns b with other help text, for places that describe the same keys differently.
func relabel(b key.Binding, help, desc string) key.Binding {
	b.SetHelp(help, desc)
	return b
}

// newScrollView is a viewport driven by the shared scroll bindings; top, gg and bottom are handled by scroll.
func newScrollView() viewport.Model {
	vp := viewport.New()
	vp.KeyMap = viewport.KeyMap{
		Down:         keys.Down,
		Up:           keys.Up,
		HalfPageDown: keys.HalfPageDown,
		HalfPageUp:   keys.HalfPageUp,
	}
	return vp
}

// scroll applies a scroll key to vp and reports whether it was one. g after a pending g jumps to the top.
func scroll(vp *viewport.Model, msg tea.KeyPressMsg, pendingG bool) bool {
	switch {
	case key.Matches(msg, keys.Bottom):
		vp.GotoBottom()
	case key.Matches(msg, keys.Top), pendingG && key.Matches(msg, keys.GG):
		vp.GotoTop()
	case key.Matches(msg, keys.Down, keys.Up, keys.HalfPageDown, keys.HalfPageUp):
		*vp, _ = vp.Update(msg)
	default:
		return false
	}
	return true
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	pendingG := m.pendingG
	m.pendingG = false
	m = m.syncViewport()

	if key.Matches(msg, keys.ForceQuit) {
		return m, tea.Quit
	}
	if m.dialog.kind != dialogNone {
		return m.handleDialogKey(msg)
	}
	if key.Matches(msg, keys.Help) {
		m.help = !m.help
		m.helpView.SetYOffset(0)
		return m, nil
	}
	if m.help {
		switch {
		case key.Matches(msg, keys.Cancel, keys.Quit):
			m.help = false
		case !scroll(&m.helpView, msg, pendingG):
			m.pendingG = key.Matches(msg, keys.GG)
		}
		return m, nil
	}

	switch {
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, keys.Tabs):
		m.tab = Tab(msg.String()[0] - '1')
		return m, nil
	case key.Matches(msg, keys.Refresh):
		m.agents = m.agents.reload(m.deps.Agents(m.deps.Now()))
		m.info = m.refreshInfo()
		return m.restartStatus()
	}
	if m.tab == TabAgents {
		return m.handleAgentsKey(msg, pendingG), nil
	}
	if m.tab == TabOverview && key.Matches(msg, keys.Window) {
		m.overview.window = nextWindow(m.overview.window)
		return m.restartStatus()
	}

	if !scroll(m.tabView(), msg, pendingG) {
		m.pendingG = key.Matches(msg, keys.GG)
	}
	return m, nil
}

// tabView is the scrollable pane of the Overview, Info, or About tab.
func (m *Model) tabView() *viewport.Model {
	switch m.tab {
	case TabInfo:
		return &m.info.view
	case TabAbout:
		return &m.about
	default:
		return &m.overview.view
	}
}

// handleWheel scrolls the help overlay when it is open, else the active tab's pane; the log pane follows
// again once the wheel reaches its bottom. Dialogs ignore the wheel.
func (m Model) handleWheel(msg tea.MouseWheelMsg) Model {
	m.pendingG = false
	m = m.syncViewport()
	switch {
	case m.dialog.kind != dialogNone:
	case m.help:
		m.helpView, _ = m.helpView.Update(msg)
	case m.tab == TabAgents:
		m.agents.view, _ = m.agents.view.Update(msg)
		m.agents.follow = m.agents.view.AtBottom()
	default:
		vp := m.tabView()
		*vp, _ = vp.Update(msg)
	}
	return m
}

func (m Model) handleAgentsKey(msg tea.KeyPressMsg, pendingG bool) Model {
	s := m.agents
	switch {
	case key.Matches(msg, keys.Wrap):
		s.wrap = !s.wrap
	case key.Matches(msg, keys.Follow):
		s.follow = !s.follow
	case key.Matches(msg, keys.SwitchPane):
		s.focusLog = !s.focusLog
	case key.Matches(msg, keys.PrevRun):
		s = s.stepRun(-1)
	case key.Matches(msg, keys.NextRun):
		s = s.stepRun(1)
	case key.Matches(msg, keys.Run, keys.StopRun, keys.Cron, keys.StopCron, keys.StartScheduler, keys.Actions):
		return m.agentAction(msg.String())
	case key.Matches(msg, keys.HalfPageDown, keys.HalfPageUp):
		return m.scrollLog(msg, pendingG)
	case !s.focusLog:
		return m.handleSidebarKey(msg, pendingG)
	case key.Matches(msg, keys.FocusSidebar):
		s.focusLog = false
	default:
		return m.scrollLog(msg, pendingG)
	}
	m.agents = s
	return m
}

func (m Model) handleSidebarKey(msg tea.KeyPressMsg, pendingG bool) Model {
	s := m.agents
	switch {
	case key.Matches(msg, keys.Down):
		s = s.selectAgent(s.selected + 1)
	case key.Matches(msg, keys.Up):
		s = s.selectAgent(s.selected - 1)
	case key.Matches(msg, keys.Bottom):
		s = s.selectAgent(len(s.list) - 1)
	case key.Matches(msg, keys.Top), pendingG && key.Matches(msg, keys.GG):
		s = s.selectAgent(0)
	case key.Matches(msg, keys.GG):
		m.pendingG = true
	case key.Matches(msg, keys.FocusLog):
		s.focusLog = true
	}
	m.agents = s
	return m
}

// scrollLog scrolls the log pane; reaching the bottom turns follow back on, leaving it turns follow off.
func (m Model) scrollLog(msg tea.KeyPressMsg, pendingG bool) Model {
	if !scroll(&m.agents.view, msg, pendingG) {
		m.pendingG = key.Matches(msg, keys.GG)
		return m
	}
	m.agents.follow = m.agents.view.AtBottom()
	return m
}
