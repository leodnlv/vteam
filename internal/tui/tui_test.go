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

func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		next, _ := m.handleKey(k)
		m = next.(Model)
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
	m = update(t, m, statusMsg{lines: []string{"vteam ▸ status", "\n━━ Running", "  · nothing running"}, started: fixedNow})
	out := screen(m)
	if strings.Contains(out, "vteam ▸ status") {
		t.Error("status banner should be dropped; the header replaces it")
	}
	if !strings.Contains(out, "━━ Running") || !strings.Contains(out, "updated 10:00:00") {
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
	}{{"2", TabAgents}, {"3", TabInfo}, {"tab", TabOverview}, {"shift+tab", TabInfo}, {"1", TabOverview}}
	for _, c := range cases {
		m = press(t, m, c.key)
		if m.tab != c.want {
			t.Fatalf("after %q tab = %v, want %v", c.key, m.tab, c.want)
		}
	}
}

func TestOverviewScrollingClampsAndSupportsVimKeys(t *testing.T) {
	m := newTestModel(t, nil)
	m = update(t, m, statusMsg{lines: statusLines(50), started: fixedNow})
	maxOffset := 50 - m.bodyHeight()

	m = press(t, m, "j", "j")
	if m.overview.offset != 2 {
		t.Fatalf("j j offset = %d", m.overview.offset)
	}
	m = press(t, m, "G")
	if m.overview.offset != maxOffset {
		t.Fatalf("G offset = %d, want %d", m.overview.offset, maxOffset)
	}
	m = press(t, m, "k")
	if m.overview.offset != maxOffset-1 {
		t.Fatalf("k after G offset = %d, want %d", m.overview.offset, maxOffset-1)
	}
	m = press(t, m, "g", "g")
	if m.overview.offset != 0 {
		t.Fatalf("gg offset = %d", m.overview.offset)
	}
	m = press(t, m, "ctrl+d")
	if m.overview.offset != m.bodyHeight()/2 {
		t.Fatalf("ctrl+d offset = %d", m.overview.offset)
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

func TestQuitKeys(t *testing.T) {
	for _, key := range []string{"q", "ctrl+c"} {
		_, cmd := newTestModel(t, nil).handleKey(key)
		if cmd == nil {
			t.Fatalf("%q returned no command", key)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%q did not quit", key)
		}
	}
}
