// Package palette maps the roles vteam colors text by to terminal colors.
// Every command (through internal/ansi) and the TUI (through lipgloss) read their colors from here,
// so changing a role here restyles all of vteam.
package palette

import (
	"image/color"
	"strconv"
	"strings"

	xansi "github.com/charmbracelet/x/ansi"
)

// Color is "" for the terminal's default color, an ANSI color number ("0"-"15" follow the terminal
// theme, "16"-"255" are the fixed 256-color cube), or a "#rrggbb" truecolor hex. Hex colors are
// downgraded to the nearest 256 or 16 color on terminals without truecolor.
type Color string

const Default Color = ""

// The values are tuned for dark terminal backgrounds.
var (
	// Brand colors the banner badge, the active tab, selections, dialog borders, and focused pane borders, which blend it into Accent.
	Brand = Color("#6b50ff")
	// Accent colors section markers, key hints, and the git branch, and is the far end of Brand's gradients.
	Accent = Color("#ff60ff")
	// OnBrand is the text color on a Brand background.
	OnBrand = Color("#fffaf1")
	// OnBright is the text color on a bright background, such as the live and severity chips.
	OnBright = Color("#1b1a22")
	// Muted colors secondary text: the time gutter, field labels, pids, durations, URLs, hints, tool details.
	Muted = Color("#8a879c")
	// Rule colors section rules, the gutter bar, and the borders of unfocused panes.
	Rule = Color("#4a4660")
	// Key colors key names in the TUI's hints and dialogs.
	Key = Accent

	Info   = Color("#8b75ff")
	OK     = Color("#00ffb2")
	Warn   = Color("#e8fe96")
	Fail   = Color("#ff577d")
	Claude = Accent
	Tool   = Color("#ff985a")
	Live   = OK
	Cron   = Color("#00a4ff")
	Branch = Accent

	// Surface is the background of the TUI's header and footer bands.
	Surface = Color("#272533")
)

// Value returns c as a color, and false for Default or an invalid color.
func (c Color) Value() (color.Color, bool) {
	if hex, ok := strings.CutPrefix(string(c), "#"); ok {
		rgb, err := strconv.ParseUint(hex, 16, 32)
		if err != nil || len(hex) != 6 {
			return nil, false
		}
		return color.RGBA{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 0xff}, true
	}
	n, err := strconv.Atoi(string(c))
	switch {
	case err != nil || n < 0 || n > 255:
		return nil, false
	case n < 16:
		return xansi.BasicColor(n), true
	default:
		return xansi.IndexedColor(n), true
	}
}
