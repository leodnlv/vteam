package tui

import (
	"fmt"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/leodnlv/vteam/internal/log"
)

const sidebarWidth = 26

var (
	styleSelected      = lipgloss.NewStyle().Reverse(true)
	styleSelectedBlur  = lipgloss.NewStyle().Background(lipgloss.BrightBlack).Foreground(lipgloss.White)
	styleTitleFocus    = lipgloss.NewStyle().Foreground(lipgloss.Cyan).Bold(true)
	styleTitleBlur     = lipgloss.NewStyle().Foreground(lipgloss.BrightBlack).Bold(true)
	styleDividerFocus  = lipgloss.NewStyle().Foreground(lipgloss.Cyan)
	styleToolName      = lipgloss.NewStyle().Foreground(lipgloss.Yellow).Bold(true)
	styleFinishedOK    = lipgloss.NewStyle().Foreground(lipgloss.Green).Bold(true)
	styleFinishedError = lipgloss.NewStyle().Foreground(lipgloss.Red).Bold(true)
)

type agentsState struct {
	list     []Agent
	selected int
	focusLog bool
	// runIndex indexes the selected agent's Logs; -1 follows the newest run.
	runIndex int
	offset   int
	follow   bool
	wrap     bool
	log      logFile
}

func newAgentsState() agentsState {
	return agentsState{runIndex: -1, follow: true, wrap: true}
}

func (s agentsState) current() (Agent, bool) {
	if s.selected < 0 || s.selected >= len(s.list) {
		return Agent{}, false
	}
	return s.list[s.selected], true
}

func (s agentsState) logPath() string {
	a, ok := s.current()
	if !ok || len(a.Logs) == 0 {
		return ""
	}
	if s.runIndex < 0 || s.runIndex >= len(a.Logs) {
		return a.Logs[len(a.Logs)-1]
	}
	return a.Logs[s.runIndex]
}

func (s agentsState) onLatestRun() bool {
	a, ok := s.current()
	return ok && (s.runIndex < 0 || s.runIndex >= len(a.Logs)-1)
}

// reload replaces the agent list, keeping the selection on the same agent name, and rereads the shown log.
func (s agentsState) reload(list []Agent) agentsState {
	name := ""
	if a, ok := s.current(); ok {
		name = a.Name
	}
	s.list = list
	s.selected = min(s.selected, max(0, len(list)-1))
	for i, a := range list {
		if a.Name == name {
			s.selected = i
			break
		}
	}
	s.log = readLog(s.logPath(), s.log)
	return s
}

func (s agentsState) selectAgent(i int) agentsState {
	s.selected = max(0, min(i, len(s.list)-1))
	s.runIndex = -1
	s.follow = true
	s.log = readLog(s.logPath(), s.log)
	return s
}

func (s agentsState) stepRun(dir int) agentsState {
	a, ok := s.current()
	if !ok || len(a.Logs) == 0 {
		return s
	}
	idx := s.runIndex
	if idx < 0 {
		idx = len(a.Logs) - 1
	}
	idx = max(0, idx+dir)
	if idx >= len(a.Logs)-1 {
		idx = -1
	}
	s.runIndex = idx
	s.follow = true
	s.log = readLog(s.logPath(), s.log)
	return s
}

func (m Model) logPaneWidth() int {
	return max(10, m.width-sidebarWidth-1)
}

func (m Model) logBodyHeight() int {
	return max(1, m.bodyHeight()-2)
}

func (m Model) logRows() []string {
	width := m.logPaneWidth() - 1
	var rows []string
	for _, raw := range m.agents.log.lines {
		styled := colorizeLog(raw)
		if m.agents.wrap {
			rows = append(rows, wrapHanging(styled, width, hangingIndent(raw))...)
		} else {
			rows = append(rows, styled)
		}
	}
	return rows
}

// logOffset is the first visible row: the bottom while following, otherwise the clamped scroll position.
func (m Model) logOffset(total int) int {
	height := m.logBodyHeight()
	if m.agents.follow {
		return max(0, total-height)
	}
	return clampOffset(m.agents.offset, total, height)
}

func (m Model) renderAgents() []string {
	height := m.bodyHeight()
	sidebar := m.renderSidebar(height)
	pane := m.renderLogPane(height)
	divider := styleGray.Render("│")
	if m.agents.focusLog {
		divider = styleDividerFocus.Render("│")
	}
	lines := make([]string, height)
	for i := range lines {
		lines[i] = sidebar[i] + divider + pane[i]
	}
	return lines
}

func (m Model) renderSidebar(height int) []string {
	s := m.agents
	title := styleTitleFocus
	if s.focusLog {
		title = styleTitleBlur
	}
	lines := []string{fit(title.Render(" AGENTS"), sidebarWidth), fit(" "+rule(sidebarWidth-2), sidebarWidth)}

	for i, a := range s.list {
		glyph, age := styleGray.Render("○"), styleGray.Render(lastRunAge(a, m))
		switch {
		case a.Running != nil:
			glyph, age = styleGreen.Render("●"), styleGreen.Render(log.FormatAge(m.deps.Now().Sub(a.Running.Time())))
		case a.LoadError != "":
			glyph = styleRed.Render("✗")
		}
		row := spread(" "+glyph+" "+log.Truncate(a.Name, sidebarWidth-12), age+" ", sidebarWidth)
		switch {
		case i != s.selected:
			lines = append(lines, fit(row, sidebarWidth))
		case s.focusLog:
			lines = append(lines, styleSelectedBlur.Render(fit(xansi.Strip(row), sidebarWidth)))
		default:
			lines = append(lines, styleSelected.Render(fit(xansi.Strip(row), sidebarWidth)))
		}
	}

	if a, ok := s.current(); ok {
		lines = append(lines, "", fit(" "+rule(sidebarWidth-2), sidebarWidth))
		for _, d := range m.agentDetails(a) {
			lines = append(lines, fit(" "+d, sidebarWidth))
		}
	}
	return window(lines, 0, height, sidebarWidth)
}

func lastRunAge(a Agent, m Model) string {
	if len(a.Logs) == 0 {
		return "—"
	}
	started, ok := logStartedAt(a.Logs[len(a.Logs)-1])
	if !ok {
		return "—"
	}
	return log.FormatAge(m.deps.Now().Sub(started))
}

func (m Model) agentDetails(a Agent) []string {
	var lines []string
	field := func(label, value string) { lines = append(lines, styleGray.Render(log.PadEnd(label, 7))+value) }
	if a.Running != nil {
		field("status", styleGreen.Render(fmt.Sprintf("running · pid %d", a.Running.PID)))
	} else {
		field("status", styleGray.Render("idle"))
	}
	if a.Config != nil && a.Config.Model != "" {
		field("model", a.Config.Model)
	}
	if a.Config != nil && a.Config.Input != "" {
		field("input", a.Config.Input)
	}
	if !a.NextRun.IsZero() {
		field("next", log.FormatTimeOfDay(a.NextRun.Local(), m.deps.Now().Local()))
	}
	if a.LoadError != "" {
		lines = append(lines, styleRed.Render(firstLine(a.LoadError)))
	}
	return lines
}

func (m Model) renderLogPane(height int) []string {
	width := m.logPaneWidth()
	a, ok := m.agents.current()
	path := m.agents.logPath()
	if !ok || path == "" {
		empty := "no agents found"
		if ok {
			empty = "no logs yet for " + styleBold.Render(a.Name)
		}
		return window([]string{" " + styleGray.Render(empty)}, 0, height, width)
	}

	parts := []string{styleBold.Render(a.Name)}
	if started, ok := logStartedAt(path); ok {
		parts = append(parts, log.FormatTimeOfDay(started.Local(), m.deps.Now().Local()))
	}
	parts = append(parts, m.runCounter(a))
	if m.agents.follow {
		parts = append(parts, styleGreen.Render("following"))
	} else {
		parts = append(parts, styleYellow.Render("paused"))
	}
	if a.Running != nil && m.agents.onLatestRun() {
		parts = append(parts, styleGreen.Render("● live"))
	}
	wrap := "nowrap"
	if m.agents.wrap {
		wrap = "wrap"
	}
	title := spread(" "+strings.Join(parts, styleGray.Render(" · ")), styleGray.Render(wrap)+" ", width)

	rows := m.logRows()
	body := window(rows, m.logOffset(len(rows)), m.logBodyHeight(), width-1)
	lines := []string{fit(title, width), fit(" "+rule(width-2), width)}
	for _, r := range body {
		lines = append(lines, " "+r)
	}
	return window(lines, 0, height, width)
}

// runCounter shows which run is on screen; it stands out in yellow when that is not the newest one.
func (m Model) runCounter(a Agent) string {
	total := len(a.Logs)
	index := total
	if m.agents.runIndex >= 0 && m.agents.runIndex < total {
		index = m.agents.runIndex + 1
	}
	label := fmt.Sprintf("run %d/%d", index, total)
	if index < total {
		return styleYellow.Bold(true).Render(label)
	}
	return styleGray.Render(label)
}

var (
	logGutter = regexp.MustCompile(`^( \d{1,2}:\d\d(?::\d\d)?  )(\S)(.*)$`)
	logDetail = regexp.MustCompile(`^(\s+)(\S+)(\s{2,})(.*)$`)
	logTool   = regexp.MustCompile(`^(\s)(\S+)(.*)$`)
)

var glyphStyles = map[string]lipgloss.Style{
	"•": lipgloss.NewStyle().Foreground(lipgloss.Blue),
	"✓": styleGreen,
	"✗": styleRed,
	"!": styleYellow,
	"⚙": styleYellow,
	"❯": styleMagenta,
}

// colorizeLog restores the colors that run logs lose when written to disk, keyed on each line's glyph.
func colorizeLog(line string) string {
	switch {
	case strings.HasPrefix(line, "━━"):
		return styleSection.Render(line)
	case strings.HasPrefix(line, "vteam ▸"):
		return styleBold.Render(line)
	case strings.HasPrefix(line, "✓ "):
		return styleFinishedOK.Render(line)
	case strings.HasPrefix(line, "✗ "):
		return styleFinishedError.Render(line)
	}
	if g := logGutter.FindStringSubmatch(line); g != nil {
		clock, glyph, rest := g[1], g[2], g[3]
		switch glyph {
		case "⚙":
			if t := logTool.FindStringSubmatch(rest); t != nil {
				rest = t[1] + styleToolName.Render(t[2]) + t[3]
			}
		case "!":
			rest = styleYellow.Render(rest)
		case "✗":
			rest = styleRed.Render(rest)
		}
		paint, ok := glyphStyles[glyph]
		if !ok {
			paint = lipgloss.NewStyle()
		}
		return styleGray.Render(clock) + paint.Render(glyph) + rest
	}
	if strings.HasPrefix(strings.TrimLeft(line, " "), "✗") {
		return styleRed.Render(line)
	}
	if d := logDetail.FindStringSubmatch(line); d != nil {
		return d[1] + styleGray.Render(d[2]) + d[3] + d[4]
	}
	return line
}

// hangingIndent lines continuation rows up under the text after a log line's clock and glyph.
func hangingIndent(raw string) int {
	if g := logGutter.FindStringSubmatch(raw); g != nil {
		return lipgloss.Width(g[1]) + lipgloss.Width(g[2]) + 1
	}
	if strings.HasPrefix(raw, "━━") {
		return -1
	}
	return len(raw) - len(strings.TrimLeft(raw, " "))
}

// wrapHanging wraps a styled line to width, indenting continuation rows by indent. indent < 0 never wraps.
func wrapHanging(styled string, width, indent int) []string {
	if indent < 0 || lipgloss.Width(styled) <= width {
		return []string{styled}
	}
	if indent >= width/2 {
		indent = 0
	}
	rows := []string{xansi.Truncate(styled, width, "")}
	rest := xansi.TruncateLeft(styled, width, "")
	pad := strings.Repeat(" ", indent)
	for _, r := range strings.Split(xansi.Hardwrap(rest, width-indent, true), "\n") {
		rows = append(rows, pad+r)
	}
	return rows
}
