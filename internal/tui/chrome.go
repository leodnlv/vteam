package tui

import (
	"fmt"
	"strings"

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
	return []string{spread(left, m.projectLabel(), m.width), rule(m.width)}
}

func (m Model) projectLabel() string {
	p := m.deps.Project
	source := " (dir)"
	if p.FromAven {
		source = " (aven)"
	}
	parts := []string{styleBold.Render(p.Name) + styleGray.Render(source)}
	if p.Label != "" {
		parts = append(parts, styleGray.Render("label "+p.Label))
	}
	if p.Branch != "" {
		parts = append(parts, styleMagenta.Render("⎇")+" "+p.Branch)
	}
	return strings.Join(parts, styleGray.Render(" · ")) + " "
}

type hint struct{ keys, label string }

var globalHints = []hint{{"1-3", "tabs"}, {"?", "help"}, {"q", "quit"}}

func (m Model) hints() []hint {
	if m.help {
		return []hint{{"j/k", "scroll"}, {"?/esc", "close help"}}
	}
	if m.tab == TabAgents {
		focus := []hint{{"j/k", "select"}, {"l/⏎", "open log"}}
		if m.agents.focusLog {
			focus = []hint{{"j/k", "scroll"}, {"h", "agents"}, {"G", "follow"}}
		}
		return append(append(focus, hint{"p/n", "prev/next run"}, hint{"w", "wrap"}), globalHints...)
	}
	return append([]hint{{"j/k", "scroll"}, {"gg/G", "top/bottom"}, {"r", "refresh"}}, globalHints...)
}

func (m Model) statusText() string {
	if m.tab != TabOverview && !m.help {
		running := 0
		for _, a := range m.agents.list {
			if a.Running != nil {
				running++
			}
		}
		if running > 0 {
			return styleGreen.Render(fmt.Sprintf("● %d running", running))
		}
		return styleGray.Render("○ idle")
	}
	if m.overview.err != "" {
		return styleRed.Render("✗ " + m.overview.err)
	}
	if m.overview.updated.IsZero() {
		return ""
	}
	return styleGray.Render("updated " + log.FormatWallClock(m.overview.updated))
}

func (m Model) renderFooter() []string {
	items := make([]string, 0, len(m.hints()))
	for _, h := range m.hints() {
		items = append(items, styleKey.Render(h.keys)+" "+styleGray.Render(h.label))
	}
	return []string{rule(m.width), spread(" "+strings.Join(items, "  "), m.statusText()+" ", m.width)}
}

var helpGroups = []struct {
	title string
	keys  [][2]string
}{
	{"Global", [][2]string{
		{"1 2 3", "jump to Overview / Agents / Info"},
		{"tab  shift+tab", "next / previous tab"},
		{"r", "refresh now"},
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
		{"l  enter  →", "focus the log"},
		{"h  esc  ←", "back to the sidebar"},
		{"p  n", "previous (older) / next (newer) run"},
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
		lines = append(lines, styleSection.Render("━━ "+g.title+" "+strings.Repeat("━", max(3, 46-len(g.title)))))
		for _, k := range g.keys {
			lines = append(lines, "  "+styleKey.Render(log.PadEnd(k[0], 17))+k[1])
		}
	}
	lines = append(lines, "", styleGray.Render("press ? or esc to close"))
	return styleHelpBox.Render(strings.Join(lines, "\n"))
}
