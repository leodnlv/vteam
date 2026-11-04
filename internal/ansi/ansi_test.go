package ansi

import "testing"

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
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "1")
	painted := Bold(Red("x"))
	if painted != "\x1b[1m\x1b[31mx\x1b[39m\x1b[22m" {
		t.Errorf("painted = %q", painted)
	}
	if Strip(painted+" "+Gray("y")) != "x y" {
		t.Errorf("Strip = %q", Strip(painted))
	}

	t.Setenv("NO_COLOR", "1")
	if Green("plain") != "plain" {
		t.Error("colors applied with NO_COLOR set")
	}
}
