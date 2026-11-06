package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/leodnlv/vteam/internal/palette"
)

func fg(c palette.Color) color.Color { return lipgloss.Color(string(c)) }

var (
	styleBold    = lipgloss.NewStyle().Bold(true)
	styleMuted   = lipgloss.NewStyle().Foreground(fg(palette.Muted))
	styleOff     = lipgloss.NewStyle().Foreground(fg(palette.Muted)).Faint(true)
	styleRule    = lipgloss.NewStyle().Foreground(fg(palette.Rule))
	styleInfo    = lipgloss.NewStyle().Foreground(fg(palette.Info))
	styleOK      = lipgloss.NewStyle().Foreground(fg(palette.OK))
	styleWarn    = lipgloss.NewStyle().Foreground(fg(palette.Warn))
	styleFail    = lipgloss.NewStyle().Foreground(fg(palette.Fail))
	styleClaude  = lipgloss.NewStyle().Foreground(fg(palette.Claude))
	styleTool    = lipgloss.NewStyle().Foreground(fg(palette.Tool))
	styleLive    = lipgloss.NewStyle().Foreground(fg(palette.Live))
	styleCron    = lipgloss.NewStyle().Foreground(fg(palette.Cron))
	styleBranch  = lipgloss.NewStyle().Foreground(fg(palette.Branch))
	styleKey     = lipgloss.NewStyle().Foreground(fg(palette.Key)).Bold(true)
	styleBrand   = lipgloss.NewStyle().Foreground(fg(palette.Brand)).Bold(true)
	styleMarker  = lipgloss.NewStyle().Foreground(fg(palette.Brand))
	styleSection = styleBold

	styleBand      = lipgloss.NewStyle().Background(fg(palette.Surface))
	styleTab       = styleMuted
	styleTabActive = lipgloss.NewStyle().Background(fg(palette.Brand)).Foreground(fg(palette.OnBrand)).Bold(true)

	styleTitleFocus   = styleBrand
	styleTitleBlur    = lipgloss.NewStyle().Foreground(fg(palette.Muted)).Bold(true)
	styleDivider      = styleRule
	styleDividerFocus = lipgloss.NewStyle().Foreground(fg(palette.Focus))

	styleSelected     = lipgloss.NewStyle().Reverse(true)
	styleSelectedBlur = lipgloss.NewStyle().Background(fg(palette.Muted)).Foreground(fg(palette.OnBrand))

	styleToolName      = lipgloss.NewStyle().Foreground(fg(palette.Tool)).Bold(true)
	styleFinishedOK    = lipgloss.NewStyle().Foreground(fg(palette.OK)).Bold(true)
	styleFinishedError = lipgloss.NewStyle().Foreground(fg(palette.Fail)).Bold(true)

	styleBox     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(fg(palette.Brand)).Padding(0, 2)
	styleBoxHead = styleBrand
	styleHelpBox = styleBox
)

// bandOpen is the escape sequence that turns on the band background.
var bandOpen = func() string {
	marked := styleBand.Render("x")
	return marked[:strings.Index(marked, "x")]
}()

// band paints a styled line of width cells on the band background, which inner style resets would otherwise end.
func band(line string, width int) string {
	line = fit(line, width)
	return bandOpen + strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+bandOpen) + "\x1b[m"
}

// section renders a section heading as the commands do: an accent marker, the bold title, and a rule up to width cells.
func section(title string, width int) string {
	return styleMarker.Render("▍") + " " + styleSection.Render(title) + "  " + styleRule.Render(strings.Repeat("─", max(3, width-lipgloss.Width(title)-4)))
}

// stretchSections redraws the section headings among lines, which commands cap at 72 columns, to width cells.
func stretchSections(lines []string, width int) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = line
		if s := logSection.FindStringSubmatch(xansi.Strip(line)); s != nil {
			out[i] = section(s[1], width)
		}
	}
	return out
}
