package tui

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/log"
)

var styleOff = lipgloss.NewStyle().Foreground(lipgloss.BrightBlack).Faint(true)

type infoState struct {
	config       *config.Config
	configErr    string
	schedulerPID int
	offset       int
}

func (m Model) refreshInfo() infoState {
	s := m.info
	s.config, s.configErr = nil, ""
	if cfg, err := m.deps.LoadConfig(); err != nil {
		s.configErr = firstLine(err.Error())
	} else {
		s.config = cfg
	}
	s.schedulerPID = m.deps.SchedulerPID()
	return s
}

func (m Model) infoLines() []string {
	var lines []string
	section := func(title string) {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.TrimPrefix(log.FormatSection(title), "\n"))
	}
	field := func(label, value string) {
		lines = append(lines, "  "+styleGray.Render(log.PadEnd(label, 14))+value)
	}

	p := m.deps.Project
	section("Project")
	source := "directory name"
	if p.FromAven {
		source = "aven tasks.project"
	}
	field("name", p.Name+" "+styleGray.Render("(from "+source+")"))
	field("directory", p.Dir)
	if p.Branch != "" {
		field("branch", p.Branch)
	}

	section("vteam.config.json")
	if cfg := m.info.config; cfg != nil {
		field("platform", string(cfg.Platform))
		field("baseBranch", cfg.BaseBranch)
		field("worktreeDir", cfg.WorktreeDir)
		field("aven project", cfg.Tasks.Project)
		field("aven label", cfg.Tasks.Label)
		if cfg.Tasks.Workspace != "" {
			field("aven workspace", cfg.Tasks.Workspace)
		}
		field("maxRetries", strconv.Itoa(cfg.Tasks.MaxRetries))
		if cfg.MergeEnsurer.Cron != "" {
			field("merge cron", cfg.MergeEnsurer.Cron)
		} else {
			field("merge cron", styleGray.Render("not scheduled"))
		}
		field("merge label", cfg.MergeEnsurer.Label)
	} else {
		field("error", styleRed.Render(m.info.configErr))
	}

	section("Scheduler")
	if pid := m.info.schedulerPID; pid > 0 {
		field("state", styleGreen.Render(fmt.Sprintf("running (pid %d)", pid)))
	} else {
		field("state", styleYellow.Render("not running")+styleGray.Render(" (start it with 'vteam cron start')"))
	}

	section("Agents")
	for _, a := range m.agents.list {
		lines = append(lines, m.agentInfo(a)...)
	}
	return lines
}

func (m Model) agentInfo(a Agent) []string {
	glyph := styleGray.Render("○")
	switch {
	case a.Running != nil:
		glyph = styleGreen.Render("●")
	case a.LoadError != "":
		glyph = styleRed.Render("✗")
	}
	lines := []string{"  " + glyph + " " + styleBold.Render(a.Name)}
	const indent = 16
	detail := func(label, value string) {
		lines = append(lines, "      "+styleGray.Render(log.PadEnd(label, 10))+value)
	}
	detailList := func(label string, items []string) {
		rows := packItems(items, max(20, m.width-indent-1))
		detail(label, rows[0])
		for _, row := range rows[1:] {
			lines = append(lines, strings.Repeat(" ", indent)+row)
		}
	}

	switch {
	case a.LoadError != "":
		lines = append(lines, "      "+styleRed.Render(firstLine(a.LoadError)))
		return lines
	case a.Builtin:
		lines = append(lines, "      "+styleGray.Render("built-in sweep (vteam ensure-merge)"))
		if !a.NextRun.IsZero() {
			detail("next", log.FormatTimeOfDay(a.NextRun.Local(), m.deps.Now().Local()))
		}
		return lines
	case a.Config == nil:
		lines = append(lines, "      "+styleGray.Render("no agent directory (logs only)"))
		return lines
	}

	c := a.Config
	detail("model", orDefault(c.Model, "default"))
	output := c.Output
	if output == "" {
		output = "generic"
	}
	detail("io", styleGray.Render("in ")+orDefault(c.Input, "—")+styleGray.Render("  out ")+output)
	if c.Cron != "" {
		next := ""
		if !a.NextRun.IsZero() {
			next = styleGray.Render("  next " + log.FormatTimeOfDay(a.NextRun.Local(), m.deps.Now().Local()))
		}
		detail("cron", c.Cron+next)
	} else {
		detail("cron", styleGray.Render("manual"))
	}
	detail("flags", strings.Join([]string{
		flag(c.Worktree, "worktree"), flag(c.ReadOnly, "readOnly"), flag(c.AutoPR, "autoPR"),
		flag(c.ResolveThreads, "resolveThreads"), flag(c.Memory != nil, "memory"), flag(c.OnFinish != nil, "onFinish"),
	}, " "))
	if len(c.TaskFilter) > 0 {
		detail("filter", formatFilter(c.TaskFilter))
	}
	if len(c.AllowedTools) > 0 {
		detailList("tools", c.AllowedTools)
	} else {
		detail("tools", styleGray.Render("Claude Code default set"))
	}
	if len(c.DisallowedTools) > 0 {
		detailList("denied", c.DisallowedTools)
	}
	return lines
}

// packItems joins items with ", " into rows no wider than width, never splitting an item.
func packItems(items []string, width int) []string {
	var rows []string
	row := ""
	for i, item := range items {
		if i < len(items)-1 {
			item += ","
		}
		switch {
		case row == "":
			row = item
		case xansi.StringWidth(row)+1+xansi.StringWidth(item) <= width:
			row += " " + item
		default:
			rows = append(rows, row)
			row = item
		}
	}
	return append(rows, row)
}

// formatFilter renders a taskFilter as "key: a | b" per key; "" stands for a key missing on the task.
func formatFilter(filter config.TaskFilter) string {
	keys := slices.Sorted(maps.Keys(filter))
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		values := make([]string, 0, len(filter[key]))
		for _, v := range filter[key] {
			if v == "" {
				v = styleGray.Render("(unset)")
			}
			values = append(values, v)
		}
		parts = append(parts, styleGray.Render(key+": ")+strings.Join(values, styleGray.Render(" | ")))
	}
	return strings.Join(parts, styleGray.Render("  ·  "))
}

func flag(on bool, label string) string {
	if on {
		return styleGreen.Render(label)
	}
	return styleOff.Render(label)
}

func orDefault(value, fallback string) string {
	if value == "" {
		return styleGray.Render(fallback)
	}
	return value
}
