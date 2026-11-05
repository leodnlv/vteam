package log

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/golden"
)

func ms(n int64) time.Duration { return time.Duration(n) * time.Millisecond }

type formatted struct {
	Banner, BannerDetail, Section, SectionLong, ListItem, ListDetail string
}

func renderFormatted() formatted {
	return formatted{
		Banner:       FormatBanner("status", ""),
		BannerDetail: FormatBanner("status", "aven: p · label: vteam"),
		Section:      FormatSection("Running"),
		SectionLong:  FormatSection(strings.Repeat("x", 80)),
		ListItem:     FormatListItem("•", "line one\nline two\n\nline four"),
		ListDetail:   FormatListDetail("worktree", ".w/x"),
	}
}

func TestFormatGolden(t *testing.T) {
	var g struct {
		FormatClock []struct {
			Ms       int64
			Expected string
		}
		FormatDuration []struct {
			Ms       int64
			Expected string
		}
		FormatAge []struct {
			Ms       int64
			Expected string
		}
		Truncate []struct {
			Text     string
			Width    int
			Expected string
		}
		Plain, Color formatted
	}
	golden.Load(t, "log", &g)

	for _, c := range g.FormatClock {
		if got := FormatClock(ms(c.Ms)); got != c.Expected {
			t.Errorf("FormatClock(%d) = %q, want %q", c.Ms, got, c.Expected)
		}
	}
	for _, c := range g.FormatDuration {
		if got := FormatDuration(ms(c.Ms)); got != c.Expected {
			t.Errorf("FormatDuration(%d) = %q, want %q", c.Ms, got, c.Expected)
		}
	}
	for _, c := range g.FormatAge {
		if got := FormatAge(ms(c.Ms)); got != c.Expected {
			t.Errorf("FormatAge(%d) = %q, want %q", c.Ms, got, c.Expected)
		}
	}
	for _, c := range g.Truncate {
		if got := Truncate(c.Text, c.Width); got != c.Expected {
			t.Errorf("Truncate(%q, %d) = %q, want %q", c.Text, c.Width, got, c.Expected)
		}
	}

	t.Setenv("NO_COLOR", "1")
	if diff := cmp.Diff(g.Plain, renderFormatted()); diff != "" {
		t.Errorf("plain formatting (-want +got):\n%s", diff)
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "1")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "xterm-256color")
	if diff := cmp.Diff(g.Color, renderFormatted()); diff != "" {
		t.Errorf("color formatting (-want +got):\n%s", diff)
	}
}

func TestFormatClock(t *testing.T) {
	for in, want := range map[int64]string{0: "00:00", 252_400: "04:12", 3_725_000: "1:02:05"} {
		if got := FormatClock(ms(in)); got != want {
			t.Errorf("FormatClock(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatDurationUsesTheLargestTwoUnits(t *testing.T) {
	for in, want := range map[int64]string{9_400: "9s", 252_000: "4m12s", 3_780_000: "1h03m"} {
		if got := FormatDuration(ms(in)); got != want {
			t.Errorf("FormatDuration(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("abcdef", 4); got != "abc…" {
		t.Errorf("got %q", got)
	}
	if got := Truncate("abc", 4); got != "abc" {
		t.Errorf("got %q", got)
	}
}

func TestFormatAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "<1m"},
		{-5 * time.Second, "<1m"},
		{12 * time.Minute, "12m"},
		{3*time.Hour + 42*time.Minute, "3h 42m"},
		{26*time.Hour + 5*time.Minute, "1d 2h"},
	}
	for _, c := range cases {
		if got := FormatAge(c.d); got != c.want {
			t.Errorf("FormatAge(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestFormatTimeOfDay(t *testing.T) {
	now := time.Date(2026, 3, 7, 18, 0, 0, 0, time.Local)
	if got := FormatTimeOfDay(time.Date(2026, 3, 7, 9, 5, 0, 0, time.Local), now); got != "09:05" {
		t.Errorf("same day = %q", got)
	}
	if got := FormatTimeOfDay(time.Date(2026, 3, 8, 0, 30, 0, 0, time.Local), now); got != "Mar 8 00:30" {
		t.Errorf("other day = %q", got)
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTestLogger(t *testing.T) (*Logger, *bytes.Buffer, *bytes.Buffer, *fakeClock) {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	var out, errOut bytes.Buffer
	clock := &fakeClock{time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)}
	return New(&out, &errOut, WithClock(clock.now)), &out, &errOut, clock
}

func TestLoggerGutterAndGlyphs(t *testing.T) {
	l, out, errOut, clock := newTestLogger(t)
	clock.t = clock.t.Add(65 * time.Second)
	l.Step("one\ntwo")
	l.OK("done")
	l.Field("branch", "vteam/x")
	l.Warn("careful")
	l.Fail("broken")

	wantOut := " 01:05 │ • one\n       │   two\n 01:05 │ ✓ done\n       │   branch    vteam/x\n"
	if diff := cmp.Diff(wantOut, out.String()); diff != "" {
		t.Errorf("stdout (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(" 01:05 │ ! careful\n 01:05 │ ✗ broken\n", errOut.String()); diff != "" {
		t.Errorf("stderr (-want +got):\n%s", diff)
	}
}

func TestLoggerWallClock(t *testing.T) {
	l, out, _, _ := newTestLogger(t)
	l.UseWallClock()
	l.Step("tick")
	if got := out.String(); got != " 10:00:00 │ • tick\n" {
		t.Errorf("got %q", got)
	}
}

func TestLoggerCopiesStrippedLinesToTheFile(t *testing.T) {
	l, out, _, _ := newTestLogger(t)
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "1")
	var file bytes.Buffer
	l.SetFile(&file)
	l.OK("saved")
	l.Warn("hmm")
	if !strings.Contains(out.String(), "\x1b[") {
		t.Error("terminal output lost its colors")
	}
	if got := file.String(); got != " 00:00 │ ✓ saved\n 00:00 │ ! hmm\n" {
		t.Errorf("file = %q", got)
	}
}

func TestToolCallSplitsTerminalAndFileLines(t *testing.T) {
	l, out, _, _ := newTestLogger(t)
	var file bytes.Buffer
	l.SetFile(&file)
	saved := TerminalWidth
	t.Cleanup(func() { TerminalWidth = saved })

	TerminalWidth = func() int { return 30 }
	l.ToolCall("Bash", "echo a very long command line\nsecond line")
	if got := out.String(); got != " 00:00 │ ⚙ Bash   echo a very…\n" {
		t.Errorf("terminal = %q", got)
	}
	if got := file.String(); got != " 00:00 │ ⚙ Bash   echo a very long command line\n       │          second line\n" {
		t.Errorf("file = %q", got)
	}

	out.Reset()
	file.Reset()
	TerminalWidth = func() int { return 0 }
	l.ToolCall("TodoWrite", "")
	if got := out.String(); got != " 00:00 │ ⚙ TodoWrite\n" {
		t.Errorf("no detail = %q", got)
	}
}

func TestToolErrorShowsTheFirstLine(t *testing.T) {
	l, out, _, _ := newTestLogger(t)
	l.ToolError("\n  Exit code 1\nmore")
	if got := out.String(); got != "       │   ✗ Exit code 1\n" {
		t.Errorf("got %q", got)
	}
}

func TestAgentExitAndFinished(t *testing.T) {
	l, out, errOut, clock := newTestLogger(t)
	d, turns, cost := 125*time.Second, 7, 0.4567
	l.AgentExit(ExitSummary{ExitCode: 0, Duration: &d, Turns: &turns, CostUSD: &cost})
	l.AgentExit(ExitSummary{ExitCode: 2})
	clock.t = clock.t.Add(90 * time.Second)
	l.Finished("coder", "")
	l.Finished("coder", "boom")

	if got := out.String(); got != "       │\n 00:00 │ ✓ claude exited 0 · 2m05s · 7 turns · $0.46\n       │\n\n✓ coder completed in 1m30s\n" {
		t.Errorf("stdout = %q", got)
	}
	if got := errOut.String(); got != " 00:00 │ ✗ claude exited 2\n\n✗ coder failed after 1m30s: boom\n" {
		t.Errorf("stderr = %q", got)
	}
}
