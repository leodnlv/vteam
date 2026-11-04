package dashboard

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/ansi"
)

func TestMain(m *testing.M) {
	os.Setenv("NO_COLOR", "1")
	os.Exit(m.Run())
}

func TestComposeFrame(t *testing.T) {
	t.Run("splits multi-line entries and appends the footer after a blank line", func(t *testing.T) {
		if diff := cmp.Diff([]string{"a", "b", "c", "", "footer"}, ComposeFrame([]string{"a", "b\nc"}, "footer", 24)); diff != "" {
			t.Error(diff)
		}
	})
	t.Run("clips the body to the terminal height and says how much is hidden", func(t *testing.T) {
		frame := ComposeFrame([]string{"1", "2", "3", "4", "5", "6"}, "footer", 6)
		for i := range frame {
			frame[i] = ansi.Strip(frame[i])
		}
		if diff := cmp.Diff([]string{"1", "2", "3", "  … 3 more line(s), enlarge the terminal", "", "footer"}, frame); diff != "" {
			t.Error(diff)
		}
	})
	t.Run("keeps every line when the terminal height is unknown", func(t *testing.T) {
		if n := len(ComposeFrame([]string{"1", "2"}, "footer", 0)); n != 4 {
			t.Errorf("len = %d", n)
		}
	})
}

func TestFooter(t *testing.T) {
	at := time.Date(2026, 1, 1, 9, 5, 7, 0, time.Local)
	if got := Footer(at, ""); got != "updated 09:05:07 · refreshes every 1s · Ctrl+C to quit" {
		t.Errorf("got %q", got)
	}
	if got := Footer(at, "aven down\ndetails"); !strings.HasPrefix(got, "✗ refresh failed: aven down  updated") {
		t.Errorf("got %q", got)
	}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func update(t *testing.T, m tea.Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func TestModelRefreshCycle(t *testing.T) {
	c := &clock{time.Date(2026, 1, 1, 10, 0, 0, 0, time.Local)}
	frames := [][]string{{"first"}, nil}
	errs := []error{nil, errors.New("aven down")}
	calls := 0
	render := func(context.Context) ([]string, error) {
		i := calls
		calls++
		return frames[i], errs[i]
	}
	m := New(context.Background(), render, c.now)

	msg := m.Init()()
	m, cmd := update(t, m, msg)
	if !strings.Contains(m.View().Content, "first") || !m.View().AltScreen {
		t.Errorf("view = %q", m.View().Content)
	}
	if cmd == nil {
		t.Fatal("no refresh scheduled")
	}

	m, cmd = update(t, m, refreshMsg{})
	m, _ = update(t, m, cmd())
	view := m.View().Content
	if !strings.Contains(view, "first") || !strings.Contains(view, "✗ refresh failed: aven down") {
		t.Errorf("failed refresh lost the last frame:\n%s", view)
	}
	if calls != 2 {
		t.Errorf("render called %d times", calls)
	}
}

func TestModelWaitsOnlyTheRestOfTheInterval(t *testing.T) {
	c := &clock{time.Date(2026, 1, 1, 10, 0, 0, 0, time.Local)}
	m := New(context.Background(), func(context.Context) ([]string, error) { return nil, nil }, c.now)
	started := c.t
	c.t = c.t.Add(3 * time.Second)
	start := time.Now()
	_, cmd := update(t, m, frameMsg{started: started})
	if msg := cmd(); msg != (refreshMsg{}) || time.Since(start) > 500*time.Millisecond {
		t.Errorf("slow refresh waited %s for %v", time.Since(start), msg)
	}
}

func TestModelClipsToTheWindowAndQuits(t *testing.T) {
	m := New(context.Background(), nil, time.Now)
	m.body = []string{"1", "2", "3", "4", "5"}
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 4})
	if lines := strings.Split(m.View().Content, "\n"); len(lines) != 4 {
		t.Errorf("frame has %d lines", len(lines))
	}
	for _, key := range []tea.KeyPressMsg{{Code: 'c', Mod: tea.ModCtrl}, {Code: 'q', Text: "q"}} {
		if _, cmd := update(t, m, key); cmd == nil || cmd() != tea.Quit() {
			t.Errorf("%v does not quit", key)
		}
	}
	if _, cmd := update(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"}); cmd != nil {
		t.Error("x quits")
	}
}
