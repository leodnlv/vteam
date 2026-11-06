package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/leodnlv/vteam/internal/config"
)

var fixedNow = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

func newTestModel(t *testing.T, render func(context.Context) ([]string, error)) Model {
	t.Helper()
	m := New(context.Background(), Deps{
		Project:      Project{Name: "my-app", FromAven: true, Label: "vteam", Branch: "main"},
		RenderStatus: render,
		Agents:       func(time.Time) []Agent { return nil },
		LoadConfig:   func() (*config.Config, error) { return nil, errors.New("no config") },
		SchedulerPID: func() int { return 0 },
		Now:          func() time.Time { return fixedNow },
	})
	return update(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
}

func update(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(Model)
}

// keyPress builds the key press whose String() is name, e.g. "j", "G", "ctrl+d", "space", "enter".
func keyPress(name string) tea.KeyPressMsg {
	named := map[string]rune{
		"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "backspace": tea.KeyBackspace,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"home": tea.KeyHome, "end": tea.KeyEnd, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	}
	switch {
	case name == "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case strings.HasPrefix(name, "ctrl+"):
		return tea.KeyPressMsg{Code: rune(name[len("ctrl+")]), Mod: tea.ModCtrl}
	}
	if code, ok := named[name]; ok {
		return tea.KeyPressMsg{Code: code}
	}
	r := []rune(name)[0]
	return tea.KeyPressMsg{Code: r, Text: name}
}

func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		msg := keyPress(k)
		if got := msg.String(); got != k {
			t.Fatalf("keyPress(%q).String() = %q", k, got)
		}
		m = update(t, m, msg)
	}
	return m
}

func screen(m Model) string {
	return xansi.Strip(m.View().Content)
}

func statusLines(n int) []string {
	lines := []string{"vteam ▸ status   banner"}
	for i := range n {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	return lines
}

func TestHeaderShowsTabsAndProject(t *testing.T) {
	m := newTestModel(t, nil)
	first := strings.Split(screen(m), "\n")[0]
	for _, want := range []string{"vteam ▸", "1 Overview", "2 Agents", "3 Info", "my-app (aven)", "label vteam", "⎇ main"} {
		if !strings.Contains(first, want) {
			t.Errorf("header %q is missing %q", first, want)
		}
	}
}

func TestViewFillsTheTerminalExactly(t *testing.T) {
	m := newTestModel(t, nil)
	m = update(t, m, statusMsg{lines: statusLines(50), started: fixedNow})
	lines := strings.Split(screen(m), "\n")
	if len(lines) != 20 {
		t.Fatalf("got %d lines, want 20", len(lines))
	}
	for i, l := range lines {
		if w := xansi.StringWidth(l); w > 100 {
			t.Errorf("line %d is %d cells wide", i, w)
		}
	}
}

func TestOverviewDropsBannerAndShowsStatus(t *testing.T) {
	m := newTestModel(t, nil)
	m = update(t, m, statusMsg{lines: []string{"vteam ▸ status", "\n▍ Running  ───", "  · nothing running"}, started: fixedNow})
	out := screen(m)
	if strings.Contains(out, "vteam ▸ status") {
		t.Error("status banner should be dropped; the header replaces it")
	}
	if !strings.Contains(out, "▍ Running") || !strings.Contains(out, "updated 10:00:00") {
		t.Errorf("overview is missing status content:\n%s", out)
	}
}

func TestRefreshErrorKeepsLastFrame(t *testing.T) {
	m := newTestModel(t, nil)
	m = update(t, m, statusMsg{lines: statusLines(3), started: fixedNow})
	m = update(t, m, statusMsg{err: errors.New("aven exploded\nstack"), started: fixedNow})
	out := screen(m)
	if !strings.Contains(out, "line 2") || !strings.Contains(out, "✗ aven exploded") || strings.Contains(out, "stack") {
		t.Errorf("want last frame plus first error line:\n%s", out)
	}
}

func TestTabSwitching(t *testing.T) {
	m := newTestModel(t, nil)
	cases := []struct {
		key  string
		want Tab
	}{{"2", TabAgents}, {"3", TabInfo}, {"tab", TabInfo}, {"shift+tab", TabInfo}, {"4", TabAbout}, {"1", TabOverview}}
	for _, c := range cases {
		m = press(t, m, c.key)
		if m.tab != c.want {
			t.Fatalf("after %q tab = %v, want %v", c.key, m.tab, c.want)
		}
	}
}

func TestTabTogglesAgentsFocus(t *testing.T) {
	m := press(t, newTestModel(t, nil), "2")
	for i, want := range []bool{true, false, true} {
		key := "tab"
		if i == 1 {
			key = "shift+tab"
		}
		m = press(t, m, key)
		if m.tab != TabAgents || m.agents.focusLog != want {
			t.Fatalf("after %q: tab = %v, focusLog = %v, want focusLog %v", key, m.tab, m.agents.focusLog, want)
		}
	}
}

func TestOverviewScrollingClampsAndSupportsVimKeys(t *testing.T) {
	m := newTestModel(t, nil)
	m = update(t, m, statusMsg{lines: statusLines(50), started: fixedNow})
	maxOffset := 50 - m.bodyHeight()

	m = press(t, m, "j", "j")
	if m.overview.view.YOffset() != 2 {
		t.Fatalf("j j offset = %d", m.overview.view.YOffset())
	}
	m = press(t, m, "G")
	if m.overview.view.YOffset() != maxOffset {
		t.Fatalf("G offset = %d, want %d", m.overview.view.YOffset(), maxOffset)
	}
	m = press(t, m, "k")
	if m.overview.view.YOffset() != maxOffset-1 {
		t.Fatalf("k after G offset = %d, want %d", m.overview.view.YOffset(), maxOffset-1)
	}
	m = press(t, m, "g", "g")
	if m.overview.view.YOffset() != 0 {
		t.Fatalf("gg offset = %d", m.overview.view.YOffset())
	}
	m = press(t, m, "ctrl+d")
	if m.overview.view.YOffset() != m.bodyHeight()/2 {
		t.Fatalf("ctrl+d offset = %d", m.overview.view.YOffset())
	}
}

func TestHelpOverlayTogglesAndSwallowsKeys(t *testing.T) {
	m := newTestModel(t, nil)
	m = press(t, m, "?")
	if !m.help || !strings.Contains(screen(m), "toggle this help") {
		t.Fatal("? should open help")
	}
	m = press(t, m, "2")
	if m.tab != TabOverview {
		t.Fatal("keys behind the help overlay should be ignored")
	}
	m = press(t, m, "esc")
	if m.help {
		t.Fatal("esc should close help")
	}
}

func TestHelpOverlayScrollsWhenTallerThanTheBody(t *testing.T) {
	m := press(t, newTestModel(t, nil), "?")
	if out := screen(m); !strings.Contains(out, "▍ Global") || strings.Contains(out, "press ? or esc to close") {
		t.Fatalf("a 20-row terminal should show the top of the help and cut the rest:\n%s", out)
	}
	m = press(t, m, "G")
	if out := screen(m); !strings.Contains(out, "press ? or esc to close") || strings.Contains(out, "▍ Global") {
		t.Fatalf("G should scroll the help to its end:\n%s", out)
	}
	m = press(t, m, "g", "g", "j")
	if m.helpView.YOffset() != 1 || !m.help {
		t.Fatalf("gg j offset = %d, help open = %v", m.helpView.YOffset(), m.help)
	}
	m = press(t, m, "?", "?")
	if m.helpView.YOffset() != 0 {
		t.Fatal("reopening help should start at the top")
	}
}

func TestQuitKeys(t *testing.T) {
	for _, key := range []string{"q", "ctrl+c"} {
		_, cmd := newTestModel(t, nil).Update(keyPress(key))
		if cmd == nil {
			t.Fatalf("%q returned no command", key)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%q did not quit", key)
		}
	}
}

func wheel(t *testing.T, m Model, button tea.MouseButton, times int) Model {
	t.Helper()
	for range times {
		m = update(t, m, tea.MouseWheelMsg{Button: button})
	}
	return m
}

func TestViewEnablesMouseWheel(t *testing.T) {
	if mode := newTestModel(t, nil).View().MouseMode; mode != tea.MouseModeCellMotion {
		t.Fatalf("MouseMode = %v, want cell motion", mode)
	}
}

func TestMouseWheelScrollsTheActiveTab(t *testing.T) {
	m := newTestModel(t, nil)
	m = update(t, m, statusMsg{lines: statusLines(50), started: fixedNow})
	m = wheel(t, m, tea.MouseWheelDown, 2)
	if m.overview.view.YOffset() != 6 {
		t.Fatalf("two wheel notches down offset = %d, want 6", m.overview.view.YOffset())
	}
	m = wheel(t, m, tea.MouseWheelUp, 1)
	if m.overview.view.YOffset() != 3 {
		t.Fatalf("wheel up offset = %d, want 3", m.overview.view.YOffset())
	}
}

func TestMouseWheelScrollsTheHelpOverlayNotTheTab(t *testing.T) {
	m := newTestModel(t, nil)
	m = update(t, m, statusMsg{lines: statusLines(50), started: fixedNow})
	m = wheel(t, press(t, m, "?"), tea.MouseWheelDown, 1)
	if m.helpView.YOffset() != 3 || m.overview.view.YOffset() != 0 {
		t.Fatalf("help offset = %d, overview offset = %d", m.helpView.YOffset(), m.overview.view.YOffset())
	}
}
