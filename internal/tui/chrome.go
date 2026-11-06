package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/leodnlv/vteam/internal/log"
)

func (m Model) renderHeader() []string {
	parts := []string{styleBrand.Render("vteam ▸")}
	for i, name := range tabNames {
		label := fmt.Sprintf(" %d %s ", i+1, name)
		if Tab(i) == m.tab {
			parts = append(parts, styleTabActive.Render(label))
		} else {
			parts = append(parts, styleTab.Render(label))
		}
	}
	left := " " + strings.Join(parts, " ")
	return []string{band(spread(left, m.projectLabel(), m.width), m.width), rule(m.width)}
}

func (m Model) projectLabel() string {
	p := m.deps.Project
	source := " (dir)"
	if p.FromAven {
		source = " (aven)"
	}
	parts := []string{styleBold.Render(p.Name) + styleMuted.Render(source)}
	if p.Label != "" {
		parts = append(parts, styleMuted.Render("label "+p.Label))
	}
	if p.Branch != "" {
		parts = append(parts, styleBranch.Render("⎇")+" "+p.Branch)
	}
	return strings.Join(parts, styleMuted.Render(" · ")) + " "
}

// hints are the footer's key hints for what is on screen.
func (m Model) hints() []key.Binding {
	global := []key.Binding{relabel(keys.Tabs, "1-4", "tabs"), relabel(keys.Help, "?", "help"), relabel(keys.Quit, "q", "quit")}
	scrollKeys := relabel(keys.Down, "j/k", "scroll")
	if m.help {
		return []key.Binding{scrollKeys, relabel(keys.Cancel, "?/esc", "close help")}
	}
	if m.tab == TabAgents && m.dialog.kind != dialogNone {
		switch m.dialog.kind {
		case dialogMenu:
			return []key.Binding{relabel(keys.Down, "j/k", "move"), relabel(keys.Confirm, "⏎", "choose"), relabel(keys.Cancel, "esc", "close")}
		case dialogConfirm:
			return []key.Binding{relabel(keys.Confirm, "⏎", "run"), keys.Cancel}
		case dialogRun:
			return []key.Binding{relabel(keys.Confirm, "⏎", "run"), keys.NewLine, keys.Cancel}
		default:
			return []key.Binding{keys.Confirm, keys.SaveDefault, keys.Cancel}
		}
	}
	if m.tab == TabAgents {
		focus := []key.Binding{relabel(keys.Down, "j/k", "select"), relabel(keys.Actions, "space", "actions"), relabel(keys.FocusLog, "tab/l/⏎", "open log")}
		if m.agents.focusLog {
			focus = []key.Binding{scrollKeys, relabel(keys.FocusSidebar, "tab/h", "agents"), relabel(keys.Bottom, "G", "follow")}
		}
		return append(append(focus, relabel(keys.PrevRun, "p/n", "prev/next run"), relabel(keys.Wrap, "w", "wrap")), global...)
	}
	return append([]key.Binding{scrollKeys, relabel(keys.GG, "gg/G", "top/bottom")}, global...)
}

// newHelp renders key bindings in the footer's colors. Its width stays unset because help.Model's own
// cut keeps adding items when the "…" lands exactly on the limit; the footer truncates instead.
func newHelp() help.Model {
	h := help.New()
	h.ShortSeparator = "  "
	h.Styles = help.Styles{
		Ellipsis:  styleMuted,
		ShortKey:  styleKey,
		ShortDesc: styleMuted,
		FullKey:   styleKey,
		FullDesc:  lipgloss.NewStyle(),
	}
	return h
}

func (m Model) statusText() string {
	if t := m.toastText(); t != "" {
		return t
	}
	if m.tab != TabOverview && !m.help {
		running := 0
		for _, a := range m.agents.list {
			if a.Running != nil {
				running++
			}
		}
		parts := []string{styleMuted.Render("○ idle")}
		if running > 0 {
			parts[0] = styleLive.Render(fmt.Sprintf("● %d running", running))
		}
		switch crons := m.enabledCrons(); {
		case crons > 0 && m.schedulerRunning():
			parts = append(parts, styleCron.Render(fmt.Sprintf("◷ %d on cron", crons)))
		case crons > 0:
			parts = append(parts, styleWarn.Render(fmt.Sprintf("◷ %d paused", crons)))
		}
		return strings.Join(parts, styleMuted.Render(" · "))
	}
	if m.overview.err != "" {
		return styleFail.Render("✗ " + m.overview.err)
	}
	if m.overview.updated.IsZero() {
		return ""
	}
	return styleMuted.Render("updated " + log.FormatWallClock(m.overview.updated))
}

func (m Model) renderFooter() []string {
	left, right := " "+newHelp().ShortHelpView(m.hints()), m.statusText()+" "
	if room := m.width - lipgloss.Width(right) - 2; lipgloss.Width(left) > room {
		left = xansi.Truncate(left, max(0, room), "…")
	}
	return []string{rule(m.width), band(spread(left, right, m.width), m.width)}
}

var helpGroups = []struct {
	title    string
	bindings []key.Binding
}{
	{"Global", []key.Binding{keys.Tabs, keys.Refresh, keys.Help, keys.Quit}},
	{"Scrolling", []key.Binding{
		relabel(keys.Down, "j k  ↓ ↑", "line down / up"),
		relabel(keys.HalfPageDown, "ctrl+d ctrl+u", "half page down / up"),
		relabel(keys.GG, "gg  G", "top / bottom"),
	}},
	{"Agents tab", []key.Binding{
		relabel(keys.Down, "j k", "select agent (sidebar) or scroll log"),
		keys.SwitchPane, keys.FocusLog, keys.FocusSidebar,
		relabel(keys.PrevRun, "p  n", "previous (older) / next (newer) run"),
		keys.Actions, keys.Run, keys.Cron, keys.StopCron, keys.StartScheduler, keys.Follow, keys.Wrap,
	}},
}

// helpBox renders every group's bindings with bubbles/help, with the key column the same width in all groups.
func helpBox() string {
	keyWidth := 0
	for _, g := range helpGroups {
		for _, b := range g.bindings {
			keyWidth = max(keyWidth, lipgloss.Width(b.Help().Key))
		}
	}
	h := newHelp()
	var lines []string
	for i, g := range helpGroups {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, section(g.title, 50))
		padded := make([]key.Binding, len(g.bindings))
		for j, b := range g.bindings {
			padded[j] = relabel(b, log.PadEnd(b.Help().Key, keyWidth+1), b.Help().Desc)
		}
		for _, line := range strings.Split(h.FullHelpView([][]key.Binding{padded}), "\n") {
			lines = append(lines, "  "+line)
		}
	}
	lines = append(lines, "", styleMuted.Render("press ? or esc to close"))
	return styleHelpBox.Render(strings.Join(lines, "\n"))
}
