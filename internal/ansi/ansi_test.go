package ansi

import (
	"strings"
	"testing"

	"github.com/leodnlv/vteam/internal/palette"
)

func forceTrueColor(t *testing.T) {
	t.Helper()
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "1")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "xterm-256color")
}

func TestEnabled(t *testing.T) {
	cases := []struct {
		name, noColor, forceColor string
		want                      bool
	}{
		{"NO_COLOR wins over FORCE_COLOR", "1", "1", false},
		{"FORCE_COLOR enables without a terminal", "", "1", true},
		{"FORCE_COLOR=0 does not force", "", "0", false},
		{"no terminal, no env", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", c.noColor)
			t.Setenv("FORCE_COLOR", c.forceColor)
			if got := Enabled(); got != c.want {
				t.Errorf("Enabled() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestPaintAndStrip(t *testing.T) {
	forceTrueColor(t)
	saved := palette.Fail
	t.Cleanup(func() { palette.Fail = saved })
	palette.Fail = "#ff7a7a"

	painted := Bold(Fail("x"))
	if painted != "\x1b[1m\x1b[38;2;255;122;122mx\x1b[39m\x1b[22m" {
		t.Errorf("painted = %q", painted)
	}
	if Strip(painted+" "+Muted("y")+" "+Badge("z")) != "x y z" {
		t.Errorf("Strip = %q", Strip(painted))
	}

	t.Setenv("NO_COLOR", "1")
	if OK("plain") != "plain" || Badge("plain") != "plain" {
		t.Error("colors applied with NO_COLOR set")
	}
}

func TestRoleFollowsPalette(t *testing.T) {
	forceTrueColor(t)
	saved := palette.Info
	t.Cleanup(func() { palette.Info = saved })

	cases := map[palette.Color]string{
		palette.Default: "x",
		"4":             "\x1b[34mx\x1b[39m",
		"12":            "\x1b[94mx\x1b[39m",
		"208":           "\x1b[38;5;208mx\x1b[39m",
		"#9d8cff":       "\x1b[38;2;157;140;255mx\x1b[39m",
		"#nope":         "x",
	}
	for c, want := range cases {
		palette.Info = c
		if got := Info("x"); got != want {
			t.Errorf("palette.Info = %q: Info(x) = %q, want %q", c, got, want)
		}
	}
}

func TestHexDowngradesWithoutTruecolor(t *testing.T) {
	forceTrueColor(t)
	t.Setenv("COLORTERM", "")
	saved := palette.Info
	t.Cleanup(func() { palette.Info = saved })
	palette.Info = "#9d8cff"

	if got := Info("x"); !strings.HasPrefix(got, "\x1b[38;5;") {
		t.Errorf("Info(x) on a 256-color terminal = %q, want a 256-color sequence", got)
	}
}
