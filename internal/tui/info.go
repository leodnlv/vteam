package tui

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/viewport"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/log"
)

type infoState struct {
	config       *config.Config
	configErr    string
	schedulerPID int
	view         viewport.Model
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
		lines = append(lines, "  "+styleMuted.Render(log.PadEnd(label, 14))+value)
	}

	p := m.deps.Project
	section("Project")
	source := "directory name"
	if p.FromAven {
		source = "aven tasks.project"
	}
	field("name", p.Name+" "+styleMuted.Render("(from "+source+")"))
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
			field("merge cron", styleMuted.Render("not scheduled"))
		}
		field("merge label", cfg.MergeEnsurer.Label)
	} else {
		field("error", styleFail.Render(m.info.configErr))
	}

	section("Scheduler")
	if pid := m.info.schedulerPID; pid > 0 {
		field("state", styleOK.Render(fmt.Sprintf("running (pid %d)", pid)))
	} else if n := m.enabledCrons(); n > 0 {
		field("state", styleWarn.Render(fmt.Sprintf("not running · %d crons paused", n))+styleMuted.Render(" (S in the Agents tab, or 'vteam cron start')"))
	} else {
		field("state", styleMuted.Render("not running · no crons enabled"))
	}
	field("crons", fmt.Sprintf("%d enabled", m.enabledCrons())+styleMuted.Render(" (space on an agent in the Agents tab)"))

	section("Agents")
	for _, a := range m.agents.list {
		lines = append(lines, m.agentInfo(a)...)
	}
	return lines
}

func (m Model) agentInfo(a Agent) []string {
	glyph := styleMuted.Render("○")
	switch {
	case a.Running != nil:
		glyph = styleLive.Render("●")
	case a.LoadError != "":
		glyph = styleFail.Render("✗")
	}
	lines := []string{"  " + glyph + " " + styleBold.Render(a.Name)}
	const indent = 16
	detail := func(label, value string) {
		lines = append(lines, "      "+styleMuted.Render(log.PadEnd(label, 10))+value)
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
		lines = append(lines, "      "+styleFail.Render(firstLine(a.LoadError)))
		return lines
	case a.Builtin:
		lines = append(lines, "      "+styleMuted.Render("built-in sweep (vteam ensure-merge)"))
		detail("cron", m.cronInfo(a))
		return lines
	case a.Config == nil:
		lines = append(lines, "      "+styleMuted.Render("no agent directory (logs only)"))
		return lines
	}

	c := a.Config
	detail("model", orDefault(c.Model, "default"))
	output := c.Output
	if output == "" {
		output = "generic"
	}
	detail("io", styleMuted.Render("in ")+orDefault(c.Input, "—")+styleMuted.Render("  out ")+output)
	detail("cron", m.cronInfo(a))
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
		detail("tools", styleMuted.Render("Claude Code default set"))
	}
	if len(c.DisallowedTools) > 0 {
		detailList("denied", c.DisallowedTools)
	}
	return lines
}

// cronInfo is the enabled cron with its next run, or "off" with the default pattern.
func (m Model) cronInfo(a Agent) string {
	if a.Cron == "" {
		if a.DefaultCron != "" {
			return styleMuted.Render("off  default " + a.DefaultCron)
		}
		return styleMuted.Render("off")
	}
	if !m.schedulerRunning() {
		return a.Cron + styleWarn.Render("  paused")
	}
	if a.NextRun.IsZero() {
		return a.Cron
	}
	return a.Cron + styleMuted.Render("  next "+log.FormatTimeOfDay(a.NextRun.Local(), m.deps.Now().Local()))
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
				v = styleMuted.Render("(unset)")
			}
			values = append(values, v)
		}
		parts = append(parts, styleMuted.Render(key+": ")+strings.Join(values, styleMuted.Render(" | ")))
	}
	return strings.Join(parts, styleMuted.Render("  ·  "))
}

func flag(on bool, label string) string {
	if on {
		return styleOK.Render(label)
	}
	return styleOff.Render(label)
}

func orDefault(value, fallback string) string {
	if value == "" {
		return styleMuted.Render(fallback)
	}
	return value
}
