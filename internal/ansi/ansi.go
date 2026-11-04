package ansi

import (
	"os"
	"regexp"

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

var (
	Bold    = paint("1", "22")
	Dim     = paint("2", "22")
	Red     = paint("31", "39")
	Green   = paint("32", "39")
	Yellow  = paint("33", "39")
	Blue    = paint("34", "39")
	Magenta = paint("35", "39")
	Cyan    = paint("36", "39")
	Gray    = paint("90", "39")
)

func Strip(text string) string {
	return pattern.ReplaceAllString(text, "")
}
