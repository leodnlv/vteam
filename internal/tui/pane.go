package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/leodnlv/vteam/internal/palette"
)

// pane frames content in a rounded border of width×height cells, with title set into the top border on the left
// and info on the right (dropped when it does not fit). A focused pane's border blends Brand into Accent from its
// top-left to its bottom-right corner.
func pane(content []string, width, height int, focused bool, title, info string) []string {
	if width < 6 || height < 2 {
		return window(content, height, width)
	}
	edge := paneBorder(width, height, focused)
	inner := width - 2
	title = fit(title, min(lipgloss.Width(title), inner-2))
	room := inner - 2 - lipgloss.Width(title)
	if lipgloss.Width(info) > room-1 {
		info = ""
	}

	var top strings.Builder
	top.WriteString(edge(0, 0, "╭") + edge(1, 0, "─") + title)
	x := 2 + lipgloss.Width(title)
	for ; x < width-2-lipgloss.Width(info); x++ {
		top.WriteString(edge(x, 0, "─"))
	}
	top.WriteString(info + edge(width-2, 0, "─") + edge(width-1, 0, "╮"))

	lines := []string{top.String()}
	for y := 1; y < height-1; y++ {
		line := ""
		if y-1 < len(content) {
			line = content[y-1]
		}
		lines = append(lines, edge(0, y, "│")+fit(line, inner)+edge(width-1, y, "│"))
	}

	var bottom strings.Builder
	bottom.WriteString(edge(0, height-1, "╰"))
	for x := 1; x < width-1; x++ {
		bottom.WriteString(edge(x, height-1, "─"))
	}
	bottom.WriteString(edge(width-1, height-1, "╯"))
	return append(lines, bottom.String())
}

func paneBorder(width, height int, focused bool) func(x, y int, edge string) string {
	if !focused {
		return func(_, _ int, edge string) string { return styleRule.Render(edge) }
	}
	steps := width + height
	blend := lipgloss.Blend1D(steps, fg(palette.Brand), fg(palette.Accent))
	return func(x, y int, edge string) string {
		t := (float64(x)/float64(width-1) + float64(y)/float64(height-1)) / 2
		return lipgloss.NewStyle().Foreground(blend[int(t*float64(steps-1)+0.5)]).Render(edge)
	}
}
