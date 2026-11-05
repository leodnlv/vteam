package ansi

import (
	"os"
	"regexp"
	"sync"

	"github.com/charmbracelet/colorprofile"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/leodnlv/vteam/internal/palette"
	"golang.org/x/term"
)

var pattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

func StdoutIsTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func Enabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if force := os.Getenv("FORCE_COLOR"); force != "" && force != "0" {
		return true
	}
	return StdoutIsTerminal() && os.Getenv("TERM") != "dumb"
}

func paint(open, closeCode string) func(string) string {
	return func(text string) string {
		if !Enabled() {
			return text
		}
		return "\x1b[" + open + "m" + text + "\x1b[" + closeCode + "m"
	}
}

// terminalProfile asks the terminal (terminfo, and tmux when inside it) how many colors it supports, once.
var terminalProfile = sync.OnceValue(func() colorprofile.Profile {
	return colorprofile.Detect(os.Stdout, os.Environ())
})

// profile is the color depth to paint with. Forced colors (no terminal) go by the environment alone.
// Callers check Enabled first, so a profile without colors means colors were forced on.
func profile() colorprofile.Profile {
	p := colorprofile.Env(os.Environ())
	if StdoutIsTerminal() {
		p = terminalProfile()
	}
	if p < colorprofile.ANSI {
		return colorprofile.TrueColor
	}
	return p
}

// sgr returns the escape sequence that sets *c (downgraded to the terminal's color depth) as the
// foreground, or as the background when background is true.
func sgr(c *palette.Color, background bool) (string, bool) {
	value, ok := c.Value()
	if !ok {
		return "", false
	}
	value = profile().Convert(value)
	if background {
		return xansi.Style{}.BackgroundColor(value).String(), true
	}
	return xansi.Style{}.ForegroundColor(value).String(), true
}

// role paints text in the palette color *c, read at call time so palette changes apply everywhere.
func role(c *palette.Color) func(string) string {
	return func(text string) string {
		if !Enabled() {
			return text
		}
		open, ok := sgr(c, false)
		if !ok {
			return text
		}
		return open + text + "\x1b[39m"
	}
}

var (
	Bold = paint("1", "22")
	Dim  = paint("2", "22")

	Brand  = role(&palette.Brand)
	Muted  = role(&palette.Muted)
	Rule   = role(&palette.Rule)
	Info   = role(&palette.Info)
	OK     = role(&palette.OK)
	Warn   = role(&palette.Warn)
	Fail   = role(&palette.Fail)
	Claude = role(&palette.Claude)
	Tool   = role(&palette.Tool)
	Live   = role(&palette.Live)
)

// Badge paints text bold in OnBrand on a Brand background.
func Badge(text string) string {
	if !Enabled() {
		return text
	}
	bg, okBg := sgr(&palette.Brand, true)
	fg, okFg := sgr(&palette.OnBrand, false)
	if !okBg || !okFg {
		return Bold(text)
	}
	return "\x1b[1m" + bg + fg + text + "\x1b[39;49;22m"
}

func Strip(text string) string {
	return pattern.ReplaceAllString(text, "")
}
