// Package dashboard shows vteam status full-screen, refreshed every second.
package dashboard

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/leodnlv/vteam/internal/ansi"
)

const RefreshInterval = time.Second

// RenderFunc renders one status frame.
type RenderFunc func(ctx context.Context) ([]string, error)

type Model struct {
	ctx     context.Context
	render  RenderFunc
	now     func() time.Time
	body    []string
	err     string
	height  int
	updated time.Time
}

func New(ctx context.Context, render RenderFunc, now func() time.Time) Model {
	return Model{ctx: ctx, render: render, now: now}
}

type frameMsg struct {
	lines   []string
	err     error
	started time.Time
}

type refreshMsg struct{}

func (m Model) refresh() tea.Msg {
	started := m.now()
	lines, err := m.render(m.ctx)
	return frameMsg{lines: lines, err: err, started: started}
}

func (m Model) Init() tea.Cmd {
	return m.refresh
}

// Update keeps the last good frame when a refresh fails. Each refresh starts at least RefreshInterval
// after the previous one started, and never before it finished.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
	case tea.KeyPressMsg:
		if s := msg.String(); s == "ctrl+c" || s == "q" {
			return m, tea.Quit
		}
	case frameMsg:
		m.updated = m.now()
		m.err = ""
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.body = msg.lines
		}
		wait := max(0, RefreshInterval-m.now().Sub(msg.started))
		return m, tea.Tick(wait, func(time.Time) tea.Msg { return refreshMsg{} })
	case refreshMsg:
		return m, m.refresh
	}
	return m, nil
}

func (m Model) View() tea.View {
	v := tea.NewView(strings.Join(ComposeFrame(m.body, Footer(m.updated, m.err), m.height), "\n"))
	v.AltScreen = true
	return v
}

// ComposeFrame splits multi-line entries and cuts the body to fit rows, leaving room for the footer.
// rows <= 0 means the height is unknown and nothing is cut.
func ComposeFrame(body []string, footer string, rows int) []string {
	var lines []string
	for _, b := range body {
		lines = append(lines, strings.Split(b, "\n")...)
	}
	if rows > 0 {
		available := max(1, rows-2)
		if len(lines) > available {
			hidden := len(lines) - available + 1
			lines = append(lines[:available-1], ansi.Gray(fmt.Sprintf("  … %d more line(s), enlarge the terminal", hidden)))
		}
	}
	return append(lines, "", footer)
}

func Footer(updated time.Time, err string) string {
	hint := ansi.Gray(fmt.Sprintf("updated %s · refreshes every %ds · Ctrl+C to quit", updated.Format("15:04:05"), int(RefreshInterval/time.Second)))
	if err == "" {
		return hint
	}
	first, _, _ := strings.Cut(err, "\n")
	return ansi.Red("✗ refresh failed: "+first) + "  " + hint
}

func Run(ctx context.Context, render RenderFunc) error {
	_, err := tea.NewProgram(New(ctx, render, time.Now), tea.WithContext(ctx)).Run()
	return err
}
