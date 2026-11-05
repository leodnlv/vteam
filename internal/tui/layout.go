package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
)

var (
	styleBrand     = lipgloss.NewStyle().Foreground(lipgloss.Cyan).Bold(true)
	styleTabActive = lipgloss.NewStyle().Background(lipgloss.Cyan).Foreground(lipgloss.Black).Bold(true)
	styleTab       = lipgloss.NewStyle().Foreground(lipgloss.BrightBlack)
	styleGray      = lipgloss.NewStyle().Foreground(lipgloss.BrightBlack)
	styleBold      = lipgloss.NewStyle().Bold(true)
	styleKey       = lipgloss.NewStyle().Foreground(lipgloss.Cyan).Bold(true)
	styleMagenta   = lipgloss.NewStyle().Foreground(lipgloss.Magenta)
	styleRed       = lipgloss.NewStyle().Foreground(lipgloss.Red)
	styleGreen     = lipgloss.NewStyle().Foreground(lipgloss.Green)
	styleYellow    = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	styleSection   = lipgloss.NewStyle().Foreground(lipgloss.Cyan).Bold(true)
	styleHelpBox   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Cyan).Padding(0, 2)
)

// fit cuts or pads a styled line to exactly width cells.
func fit(line string, width int) string {
	if width <= 0 {
		return ""
	}
	line = xansi.Truncate(line, width, "…")
	if pad := width - lipgloss.Width(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

func rule(width int) string {
	return styleGray.Render(strings.Repeat("─", max(0, width)))
}

// spread puts left and right on one line of width cells, dropping right when both do not fit.
func spread(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		return fit(left, width)
	}
	return left + strings.Repeat(" ", gap) + right
}

// window returns height lines of lines starting at offset, each fitted to width.
func window(lines []string, offset, height, width int) []string {
	out := make([]string, 0, height)
	for i := offset; i < offset+height; i++ {
		line := ""
		if i >= 0 && i < len(lines) {
			line = lines[i]
		}
		out = append(out, fit(line, width))
	}
	return out
}

func clampOffset(offset, total, height int) int {
	return max(0, min(offset, total-height))
}

func splitLines(blocks []string) []string {
	var lines []string
	for _, b := range blocks {
		lines = append(lines, strings.Split(b, "\n")...)
	}
	return lines
}

func dropLeadingBlank(lines []string) []string {
	if len(lines) > 0 && lines[0] == "" {
		return lines[1:]
	}
	return lines
}

// overlay draws box centered on top of base, with base grayed out behind it.
func overlay(base []string, box string, width, height int) []string {
	dimmed := make([]string, len(base))
	for i, line := range base {
		dimmed[i] = styleGray.Faint(true).Render(xansi.Strip(line))
	}
	x := max(0, (width-lipgloss.Width(box))/2)
	y := max(0, (height-lipgloss.Height(box))/2)
	composed := lipgloss.NewCompositor(
		lipgloss.NewLayer(strings.Join(dimmed, "\n")),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	).Render()
	return window(strings.Split(composed, "\n"), 0, height, width)
}
