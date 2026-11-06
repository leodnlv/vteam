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
	styleAccent  = lipgloss.NewStyle().Foreground(fg(palette.Accent)).Bold(true)
	styleMarker  = lipgloss.NewStyle().Foreground(fg(palette.Accent))
	styleSection = styleBold

	styleBand      = lipgloss.NewStyle().Background(fg(palette.Surface))
	styleTab       = styleMuted
	styleTabActive = lipgloss.NewStyle().Background(fg(palette.Brand)).Foreground(fg(palette.OnBrand)).Bold(true)
	styleOnBrand   = lipgloss.NewStyle().Foreground(fg(palette.OnBrand)).Bold(true)
	styleTagBlur   = lipgloss.NewStyle().Background(fg(palette.Rule)).Foreground(fg(palette.Muted)).Bold(true)
	styleLiveChip  = lipgloss.NewStyle().Background(fg(palette.Live)).Foreground(fg(palette.OnBright)).Bold(true)

	styleSelected      = lipgloss.NewStyle().Background(fg(palette.Brand)).Foreground(fg(palette.OnBrand)).Bold(true)
	styleSelectedBlur  = lipgloss.NewStyle().Background(fg(palette.Rule)).Foreground(fg(palette.OnBrand))
	styleSelectBar     = lipgloss.NewStyle().Background(fg(palette.Brand)).Foreground(fg(palette.Accent))
	styleSelectBarBlur = lipgloss.NewStyle().Background(fg(palette.Rule)).Foreground(fg(palette.Muted))

	styleToolName      = lipgloss.NewStyle().Foreground(fg(palette.Tool)).Bold(true)
	styleFinishedOK    = lipgloss.NewStyle().Foreground(fg(palette.OK)).Bold(true)
	styleFinishedError = lipgloss.NewStyle().Foreground(fg(palette.Fail)).Bold(true)

	styleBox     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(fg(palette.Brand)).Padding(0, 2)
	styleBoxHead = styleAccent
	styleHelpBox = styleBox
)

// gradient paints each cell of text in style, with its foreground (or background, when onBackground) blended across stops.
func gradient(text string, style lipgloss.Style, onBackground bool, stops ...palette.Color) string {
	runes := []rune(text)
	colors := make([]color.Color, len(stops))
	for i, c := range stops {
		colors[i] = fg(c)
	}
	blend := lipgloss.Blend1D(len(runes), colors...)
	var b strings.Builder
	for i, r := range runes {
		cell := style.Foreground(blend[i%len(blend)])
		if onBackground {
			cell = style.Background(blend[i%len(blend)])
		}
		b.WriteString(cell.Render(string(r)))
	}
	return b.String()
}

// tag renders a pane title: Brand blending into Accent behind it when its pane has focus, a muted chip otherwise.
func tag(text string, focused bool) string {
	text = " " + text + " "
	if !focused {
		return styleTagBlur.Render(text)
	}
	return gradient(text, styleOnBrand, true, palette.Brand, palette.Accent)
}

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
	fade := gradient(strings.Repeat("─", max(3, width-lipgloss.Width(title)-4)), lipgloss.NewStyle(), false, palette.Brand, palette.Rule, palette.Rule)
	return styleMarker.Render("▍") + " " + styleSection.Render(title) + "  " + fade
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
