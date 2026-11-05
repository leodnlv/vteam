package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/leodnlv/vteam/internal/log"
)

const (
	aboutAuthor  = "Leonid Danilov"
	aboutGitHub  = "github.com/leodnlv/vteam"
	aboutLicense = "MIT License"
)

const aboutDescription = "vteam is a virtual development team: Claude Code agents that review your codebase, " +
	"file their findings as tasks, and open pull requests that fix them, on a schedule and in isolated git worktrees."

var aboutPrinciples = []struct{ title, detail string }{
	{"The codebase improves even when you are not looking.",
		"Agents run on cron, find issues, and fix them without anyone at the keyboard."},
	{"Agents keep learning.",
		"Memory, task notes, and review threads carry what earlier runs learned into the next one."},
	{"Humans have points of intervention.",
		"You triage findings from inbox to todo and review every pull request before it merges."},
}

func (m Model) aboutLines() []string {
	width := max(20, m.width-4)
	var lines []string
	section := func(title string) {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.TrimPrefix(log.FormatSection(title), "\n"))
	}
	paragraph := func(first, indent, text string, style lipgloss.Style) {
		for i, line := range strings.Split(xansi.Wordwrap(text, width-len(indent), ""), "\n") {
			if i == 0 {
				lines = append(lines, first+style.Render(line))
			} else {
				lines = append(lines, indent+style.Render(line))
			}
		}
	}
	field := func(label, value string) {
		lines = append(lines, "  "+styleMuted.Render(log.PadEnd(label, 10))+value)
	}

	paragraph("  ", "  ", aboutDescription, lipgloss.NewStyle())

	section("Philosophy")
	for i, p := range aboutPrinciples {
		paragraph("  "+styleBrand.Render(fmt.Sprintf("%d.", i+1))+" ", "     ", p.title, styleBold)
		paragraph("     ", "     ", p.detail, styleMuted)
	}

	section("Author")
	field("name", aboutAuthor)
	field("github", aboutGitHub)
	field("license", aboutLicense)
	return lines
}
