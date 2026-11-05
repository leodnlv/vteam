package tui

import (
	tea "charm.land/bubbletea/v2"
)

// scrollAction turns a key into a scroll delta. top and bottom are huge deltas that clamping turns into the ends.
func scrollAction(key string, pendingG bool, halfPage int) (delta int, ok bool) {
	const huge = 1 << 30
	switch key {
	case "j", "down":
		return 1, true
	case "k", "up":
		return -1, true
	case "ctrl+d", "pgdown", "space":
		return halfPage, true
	case "ctrl+u", "pgup":
		return -halfPage, true
	case "G", "end":
		return huge, true
	case "home":
		return -huge, true
	case "g":
		if pendingG {
			return -huge, true
		}
	}
	return 0, false
}

func scrolled(offset, delta, total, height int) int {
	return clampOffset(clampOffset(offset, total, height)+delta, total, height)
}

func (m Model) handleKey(key string) (tea.Model, tea.Cmd) {
	pendingG := m.pendingG
	m.pendingG = false

	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if key == "?" {
		m.help = !m.help
		return m, nil
	}
	if m.help {
		switch key {
		case "esc", "q":
			m.help = false
		case "g":
			m.pendingG = !pendingG
		}
		return m, nil
	}

	switch key {
	case "q":
		return m, tea.Quit
	case "1", "2", "3":
		m.tab = Tab(key[0] - '1')
		return m, nil
	case "tab":
		m.tab = (m.tab + 1) % Tab(len(tabNames))
		return m, nil
	case "shift+tab":
		m.tab = (m.tab + Tab(len(tabNames)) - 1) % Tab(len(tabNames))
		return m, nil
	case "r":
		m.agents = m.agents.reload(m.deps.Agents(m.deps.Now()))
		return m, m.fetchStatus
	}
	if m.tab == TabAgents {
		return m.handleAgentsKey(key, pendingG), nil
	}

	height := m.bodyHeight()
	delta, ok := scrollAction(key, pendingG, max(1, height/2))
	if !ok {
		m.pendingG = key == "g"
		return m, nil
	}
	switch m.tab {
	case TabOverview:
		m.overview.offset = scrolled(m.overview.offset, delta, len(m.overview.lines), height)
	case TabInfo:
		m.infoOff = scrolled(m.infoOff, delta, 0, height)
	}
	return m, nil
}

func (m Model) handleAgentsKey(key string, pendingG bool) Model {
	s := m.agents
	switch key {
	case "w":
		s.wrap = !s.wrap
	case "f":
		s.follow = !s.follow
		if !s.follow {
			s.offset = m.logOffset(len(m.logRows()))
		}
	case "p":
		s = s.stepRun(-1)
	case "n":
		s = s.stepRun(1)
	case "ctrl+d", "ctrl+u", "pgdown", "pgup":
		return m.scrollLog(key, pendingG)
	default:
		if !s.focusLog {
			return m.handleSidebarKey(key, pendingG)
		}
		switch key {
		case "h", "esc", "left":
			s.focusLog = false
		default:
			return m.scrollLog(key, pendingG)
		}
	}
	m.agents = s
	return m
}

func (m Model) handleSidebarKey(key string, pendingG bool) Model {
	s := m.agents
	switch key {
	case "j", "down":
		s = s.selectAgent(s.selected + 1)
	case "k", "up":
		s = s.selectAgent(s.selected - 1)
	case "G", "end":
		s = s.selectAgent(len(s.list) - 1)
	case "home":
		s = s.selectAgent(0)
	case "g":
		if pendingG {
			s = s.selectAgent(0)
		} else {
			m.pendingG = true
		}
	case "l", "enter", "right":
		s.focusLog = true
	}
	m.agents = s
	return m
}

// scrollLog scrolls the log pane; reaching the bottom turns follow back on, leaving it turns follow off.
func (m Model) scrollLog(key string, pendingG bool) Model {
	height := m.logBodyHeight()
	delta, ok := scrollAction(key, pendingG, max(1, height/2))
	if !ok {
		m.pendingG = key == "g"
		return m
	}
	total := len(m.logRows())
	offset := clampOffset(m.logOffset(total)+delta, total, height)
	m.agents.offset = offset
	m.agents.follow = offset >= max(0, total-height)
	return m
}
