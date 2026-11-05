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
	return max(1, m.bodyHeight()-2-len(m.cronBanner()))
}

// cronBanner is the line above the log saying the selected agent runs on cron, or nothing when its cron is off.
func (m Model) cronBanner() []string {
	a, ok := m.agents.current()
	if !ok || a.Cron == "" {
		return nil
	}
	if !m.schedulerRunning() {
		return []string{styleWarn.Render("◷ cron "+a.Cron+" paused · scheduler not running") + styleMuted.Render(" (S to start it)")}
	}
	now := m.deps.Now()
	text := styleCron.Render("◷ on cron ") + styleCron.Bold(true).Render(a.Cron)
	if desc := describeCron(a.Cron); desc != "" {
		text += styleCron.Render(" · " + desc)
	}
	if !a.NextRun.IsZero() {
		text += styleCron.Render(" · next "+log.FormatTimeOfDay(a.NextRun.Local(), now.Local())) +
			styleMuted.Render(" (in "+log.FormatAge(a.NextRun.Sub(now))+")")
	}
	return []string{text}
}

func (m Model) logRows() []string {
	width := m.logPaneWidth() - 1
	var rows []string
	for _, raw := range m.agents.log.lines {
		styled := colorizeLog(raw)
		if s := logSection.FindStringSubmatch(raw); s != nil {
			styled = section(s[1], width)
		}
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
	divider := styleDivider.Render("│")
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
		glyph, age := styleMuted.Render("○"), styleMuted.Render(lastRunAge(a, m))
		switch {
		case a.Running != nil:
			glyph, age = styleLive.Render("●"), styleLive.Render(log.FormatAge(m.deps.Now().Sub(a.Running.Time())))
		case a.LoadError != "":
			glyph = styleFail.Render("✗")
		}
		if a.Cron != "" {
			clock := styleCron.Render("◷")
			if !m.schedulerRunning() {
				clock = styleWarn.Render("◷")
			}
			age = clock + " " + age
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
	field := func(label, value string) { lines = append(lines, styleMuted.Render(log.PadEnd(label, 7))+value) }
	if a.Running != nil {
		field("status", styleLive.Render(fmt.Sprintf("running · pid %d", a.Running.PID)))
	} else {
		field("status", styleMuted.Render("idle"))
	}
	switch {
	case a.Cron != "" && m.schedulerRunning():
		field("cron", styleCron.Render(a.Cron))
		if !a.NextRun.IsZero() {
			field("next", styleCron.Render(log.FormatTimeOfDay(a.NextRun.Local(), m.deps.Now().Local())))
		}
	case a.Cron != "":
		field("cron", styleWarn.Render(a.Cron+" paused"))
	case a.DefaultCron != "":
		field("cron", styleMuted.Render("off ("+a.DefaultCron+")"))
	case a.Runnable():
		field("cron", styleMuted.Render("off"))
	}
	if a.Config != nil && a.Config.Model != "" {
		field("model", a.Config.Model)
	}
	if a.Config != nil && a.Config.Input != "" {
		field("input", a.Config.Input)
	}
	if a.LoadError != "" {
		lines = append(lines, styleFail.Render(firstLine(a.LoadError)))
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
		lines := []string{}
		for _, b := range m.cronBanner() {
			lines = append(lines, " "+b, "")
		}
		return window(append(lines, " "+styleMuted.Render(empty)), 0, height, width)
	}

	parts := []string{styleBold.Render(a.Name)}
	if started, ok := logStartedAt(path); ok {
		parts = append(parts, log.FormatTimeOfDay(started.Local(), m.deps.Now().Local()))
	}
	parts = append(parts, m.runCounter(a))
	if m.agents.follow {
		parts = append(parts, styleOK.Render("following"))
	} else {
		parts = append(parts, styleWarn.Render("paused"))
	}
	if a.Running != nil && m.agents.onLatestRun() {
		parts = append(parts, styleLive.Render("● live"))
	}
	wrap := "nowrap"
	if m.agents.wrap {
		wrap = "wrap"
	}
	title := spread(" "+strings.Join(parts, styleMuted.Render(" · ")), styleMuted.Render(wrap)+" ", width)

	rows := m.logRows()
	body := window(rows, m.logOffset(len(rows)), m.logBodyHeight(), width-1)
	lines := []string{fit(title, width), fit(" "+rule(width-2), width)}
	for _, b := range m.cronBanner() {
		lines = append(lines, fit(" "+b, width))
	}
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
		return styleWarn.Bold(true).Render(label)
	}
	return styleMuted.Render(label)
}

var (
	logGutter  = regexp.MustCompile(`^ (\d{1,2}:\d\d(?::\d\d)?)( │ |  )(\S)(.*)$`)
	logBar     = regexp.MustCompile(`^(\s+│)(.*)$`)
	logDetail  = regexp.MustCompile(`^(\s+)(\S+)(\s{2,})(.*)$`)
	logTool    = regexp.MustCompile(`^(\s)(\S+)(.*)$`)
	logSection = regexp.MustCompile(`^▍ (.*?)  (─{3,})$`)
)

var glyphStyles = map[string]lipgloss.Style{
	"•": styleInfo,
	"✓": styleOK,
	"✗": styleFail,
	"!": styleWarn,
	"⚙": styleTool,
	"❯": styleClaude,
}

// colorizeLog restores the colors that run logs lose when written to disk, keyed on each line's glyph.
// It also reads logs written before the timeline gutter (" 00:12  • ") and the ▍ sections ("━━ Setup ━━").
func colorizeLog(line string) string {
	switch {
	case strings.HasPrefix(line, "━━"):
		return styleSection.Render(line)
	case logSection.MatchString(line):
		s := logSection.FindStringSubmatch(line)
		return styleMarker.Render("▍") + " " + styleSection.Render(s[1]) + "  " + styleRule.Render(s[2])
	case strings.HasPrefix(line, "vteam ▸"):
		return styleBrand.Render("vteam ▸") + styleBold.Render(strings.TrimPrefix(line, "vteam ▸"))
	case strings.HasPrefix(line, "✓ "):
		return styleFinishedOK.Render(line)
	case strings.HasPrefix(line, "✗ "):
		return styleFinishedError.Render(line)
	}
	if g := logGutter.FindStringSubmatch(line); g != nil {
		clock, sep, glyph, rest := g[1], g[2], g[3], g[4]
		switch glyph {
		case "⚙":
			if t := logTool.FindStringSubmatch(rest); t != nil {
				rest = t[1] + styleToolName.Render(t[2]) + styleMuted.Render(t[3])
			}
		case "!":
			rest = styleWarn.Render(rest)
		case "✗":
			rest = styleFail.Render(rest)
		}
		paint, ok := glyphStyles[glyph]
		if !ok {
			paint = lipgloss.NewStyle()
		}
		return " " + styleMuted.Render(clock) + styleRule.Render(sep) + paint.Render(glyph) + rest
	}
	if b := logBar.FindStringSubmatch(line); b != nil {
		return styleRule.Render(b[1]) + colorizeDetail(b[2])
	}
	return colorizeDetail(line)
}

func colorizeDetail(line string) string {
	if strings.HasPrefix(strings.TrimLeft(line, " "), "✗") {
		return styleFail.Render(line)
	}
	if d := logDetail.FindStringSubmatch(line); d != nil {
		return d[1] + styleMuted.Render(d[2]) + d[3] + d[4]
	}
	return line
}

// hangingIndent lines continuation rows up under the text after a log line's clock and glyph.
func hangingIndent(raw string) int {
	if g := logGutter.FindStringSubmatch(raw); g != nil {
		return 1 + lipgloss.Width(g[1]) + lipgloss.Width(g[2]) + lipgloss.Width(g[3]) + 1
	}
	if strings.HasPrefix(raw, "━━") || logSection.MatchString(raw) {
		return -1
	}
	if b := logBar.FindStringSubmatch(raw); b != nil {
		return lipgloss.Width(b[1]) + len(b[2]) - len(strings.TrimLeft(b[2], " "))
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
