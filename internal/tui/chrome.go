package tui

import (
	"fmt"
	"strings"

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

type hint struct{ keys, label string }

var globalHints = []hint{{"1-4", "tabs"}, {"?", "help"}, {"q", "quit"}}

func (m Model) hints() []hint {
	if m.help {
		return []hint{{"j/k", "scroll"}, {"?/esc", "close help"}}
	}
	if m.tab == TabAgents && m.dialog.kind != dialogNone {
		switch m.dialog.kind {
		case dialogMenu:
			return []hint{{"j/k", "move"}, {"⏎", "choose"}, {"esc", "close"}}
		case dialogConfirm:
			return []hint{{"⏎", "run"}, {"esc", "cancel"}}
		case dialogRun:
			return []hint{{"⏎", "run"}, {"shift+⏎/ctrl+j", "new line"}, {"esc", "cancel"}}
		default:
			return []hint{{"⏎", "confirm"}, {"tab", "save as default"}, {"esc", "cancel"}}
		}
	}
	if m.tab == TabAgents {
		focus := []hint{{"j/k", "select"}, {"space", "actions"}, {"tab/l/⏎", "open log"}}
		if m.agents.focusLog {
			focus = []hint{{"j/k", "scroll"}, {"tab/h", "agents"}, {"G", "follow"}}
		}
		return append(append(focus, hint{"p/n", "prev/next run"}, hint{"w", "wrap"}), globalHints...)
	}
	return append([]hint{{"j/k", "scroll"}, {"gg/G", "top/bottom"}}, globalHints...)
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
	items := make([]string, 0, len(m.hints()))
	for _, h := range m.hints() {
		items = append(items, styleKey.Render(h.keys)+" "+styleMuted.Render(h.label))
	}
	left, right := " "+strings.Join(items, "  "), m.statusText()+" "
	if room := m.width - lipgloss.Width(right) - 2; lipgloss.Width(left) > room {
		left = xansi.Truncate(left, max(0, room), "…")
	}
	return []string{rule(m.width), band(spread(left, right, m.width), m.width)}
}

var helpGroups = []struct {
	title string
	keys  [][2]string
}{
	{"Global", [][2]string{
		{"1 2 3 4", "jump to Overview / Agents / Info / About"},
		{"ctrl+r", "refresh now"},
		{"?", "toggle this help"},
		{"q  ctrl+c", "quit"},
	}},
	{"Scrolling", [][2]string{
		{"j k  ↓ ↑", "line down / up"},
		{"ctrl+d ctrl+u", "half page down / up"},
		{"gg  G", "top / bottom"},
	}},
	{"Agents tab", [][2]string{
		{"j k", "select agent (sidebar) or scroll log"},
		{"tab  shift+tab", "switch between the sidebar and the log"},
		{"l  enter  →", "focus the log"},
		{"h  esc  ←", "back to the sidebar"},
		{"p  n", "previous (older) / next (newer) run"},
		{"space  a", "actions menu of the selected agent"},
		{"r", "run the selected agent once"},
		{"c", "run it on cron / edit its cron"},
		{"s", "stop its cron"},
		{"S", "start the scheduler when crons are paused"},
		{"f", "toggle follow"},
		{"w", "toggle line wrap"},
	}},
}

func helpBox() string {
	var lines []string
	for i, g := range helpGroups {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, section(g.title, 50))
		for _, k := range g.keys {
			lines = append(lines, "  "+styleKey.Render(log.PadEnd(k[0], 17))+k[1])
		}
	}
	lines = append(lines, "", styleMuted.Render("press ? or esc to close"))
	return styleHelpBox.Render(strings.Join(lines, "\n"))
}
