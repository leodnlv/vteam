package tui

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
)

func TestAboutShowsPhilosophyAndAuthor(t *testing.T) {
	m := newTestModel(t, nil)
	m.height = 40
	m = press(t, m, "4")
	out := xansi.Strip(screen(m))
	for _, want := range []string{
		"4 About", "▍ Philosophy", "1. The codebase improves even when you are not looking.",
		"2. Agents keep learning.", "3. Humans have points of intervention.",
		"▍ Author", "Leonid Danilov", "github.com/leodnlv/vteam", "MIT License",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("about is missing %q:\n%s", want, out)
		}
	}
}

func TestAboutWrapsToWidth(t *testing.T) {
	m := newTestModel(t, nil)
	m.width = 40
	for _, line := range stretchSections(m.aboutLines(), m.width-1) {
		if w := xansi.StringWidth(line); w > 40 {
			t.Errorf("line is %d cells wide, want <= 40: %q", w, xansi.Strip(line))
		}
	}
}
