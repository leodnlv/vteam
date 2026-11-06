package log

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leodnlv/vteam/internal/ansi"
	"golang.org/x/term"
)

const (
	defaultWidth = 72
	labelWidth   = 10
)

// TerminalWidth returns the stdout terminal width, or 0 when stdout is not a terminal.
var TerminalWidth = func() int {
	if !ansi.StdoutIsTerminal() {
		return 0
	}
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		return 0
	}
	return width
}

func pad2(n int) string { return fmt.Sprintf("%02d", n) }

func FormatClock(elapsed time.Duration) string {
	total := max(0, int(elapsed/time.Second))
	h, m, s := total/3600, total%3600/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%s:%s", h, pad2(m), pad2(s))
	}
	return pad2(m) + ":" + pad2(s)
}

func FormatDuration(d time.Duration) string {
	total := int(math.Floor(float64(d.Milliseconds())/1000 + 0.5))
	h, m, s := total/3600, total%3600/60, total%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%sm", h, pad2(m))
	case m > 0:
		return fmt.Sprintf("%dm%ss", m, pad2(s))
	default:
		return fmt.Sprintf("%ds", s)
	}
}

func FormatAge(d time.Duration) string {
	minutes := int(max(d, 0) / time.Minute)
	if minutes < 1 {
		return "<1m"
	}
	days, hours, mins := minutes/1440, minutes%1440/60, minutes%60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

func FormatWallClock(t time.Time) string {
	return t.Format("15:04:05")
}

func FormatTimeOfDay(t, now time.Time) string {
	ty, tm, td := t.Date()
	ny, nm, nd := now.Date()
	if ty == ny && tm == nm && td == nd {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

func Truncate(text string, width int) string {
	if width <= 1 {
		return "…"
	}
	runes := []rune(text)
	if len(runes) > width {
		return string(runes[:width-1]) + "…"
	}
	return text
}

func PadEnd(text string, width int) string {
	if n := utf8.RuneCountInString(text); n < width {
		return text + strings.Repeat(" ", width-n)
	}
	return text
}

func visibleLen(text string) int {
	return utf8.RuneCountInString(ansi.Strip(text))
}

func indentContinuation(text string, indent int) string {
	pad := strings.Repeat(" ", indent)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i > 0 && line != "" {
			lines[i] = pad + line
		}
	}
	return strings.Join(lines, "\n")
}

// FormatBanner draws "vteam" as a badge in color and as plain "vteam ▸" without it.
func FormatBanner(title, detail string) string {
	return formatBanner(title, detail, ansi.Enabled())
}

func formatBanner(title, detail string, color bool) string {
	suffix := ""
	if detail != "" {
		suffix = "   " + ansi.Muted(detail)
	}
	if !color {
		return ansi.Strip("vteam ▸ " + title + suffix)
	}
	return ansi.Badge(" vteam ") + ansi.Accent(" ▸ ") + ansi.Bold(title) + suffix
}

func FormatSection(title string) string {
	width := defaultWidth
	if w := TerminalWidth(); w > 0 {
		width = min(w, defaultWidth)
	}
	head := "▍ " + title + "  "
	rule := strings.Repeat("─", max(3, width-utf8.RuneCountInString(head)))
	return "\n" + ansi.Accent("▍") + " " + ansi.Bold(title) + "  " + ansi.Rule(rule)
}

func FormatListItem(glyph, text string) string {
	return "  " + glyph + " " + indentContinuation(text, 4)
}

func FormatListDetail(label, value string) string {
	return "    " + ansi.Muted(label) + " " + value
}
