package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
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
	return styleRule.Render(strings.Repeat("─", max(0, width)))
}

// spread puts left and right on one line of width cells, dropping right when both do not fit.
func spread(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		return fit(left, width)
	}
	return left + strings.Repeat(" ", gap) + right
}

// window returns the first height lines of lines, each fitted to width and padded with blank lines.
func window(lines []string, height, width int) []string {
	out := make([]string, 0, height)
	for i := range height {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		out = append(out, fit(line, width))
	}
	return out
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
		dimmed[i] = styleMuted.Faint(true).Render(xansi.Strip(line))
	}
	x := max(0, (width-lipgloss.Width(box))/2)
	y := max(0, (height-lipgloss.Height(box))/2)
	return overlayAt(dimmed, box, x, y, width, height)
}

// overlayAt draws box on top of base with its top-left corner at column x, row y.
func overlayAt(base []string, box string, x, y, width, height int) []string {
	composed := lipgloss.NewCompositor(
		lipgloss.NewLayer(strings.Join(base, "\n")),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	).Render()
	return window(strings.Split(composed, "\n"), height, width)
}
