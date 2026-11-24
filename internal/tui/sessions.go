package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/session"
)

const (
	cleanDialogWidth  = 64
	cleanDialogRows   = 8
	cleanAgentWidth   = 16
	cleanDiscardIndex = 1
)

type sessionsCleanedMsg struct {
	res     session.CleanupResult
	discard bool
}

func (m Model) openCleanSessions() Model {
	leftovers := m.deps.AbandonedSessions(m.ctx)
	if len(leftovers) == 0 {
		return m.withToast(styleMuted.Render("no abandoned interactive sessions to clean up"))
	}
	m.dialog = dialog{kind: dialogCleanSessions, leftovers: leftovers}
	return m
}

func unsavedCount(leftovers []session.Leftover) int {
	n := 0
	for _, l := range leftovers {
		if l.Unsaved() {
			n++
		}
	}
	return n
}

func (m Model) handleCleanSessionsKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	options := 1
	if unsavedCount(m.dialog.leftovers) > 0 {
		options = 2
	}
	switch {
	case key.Matches(msg, keys.Cancel, keys.Quit):
		m.dialog = dialog{}
	case key.Matches(msg, keys.Down):
		m.dialog.cursor = min(options-1, m.dialog.cursor+1)
	case key.Matches(msg, keys.Up):
		m.dialog.cursor = max(0, m.dialog.cursor-1)
	case key.Matches(msg, keys.Confirm):
		discard := m.dialog.cursor == cleanDiscardIndex
		m.dialog = dialog{}
		m = m.withToast(styleMuted.Render("cleaning up abandoned sessions…"))
		ctx, clean := m.ctx, m.deps.CleanSessions
		return m, func() tea.Msg { return sessionsCleanedMsg{res: clean(ctx, discard), discard: discard} }
	}
	return m, nil
}

func (m Model) receiveSessionsCleaned(msg sessionsCleanedMsg) (Model, tea.Cmd) {
	res := msg.res
	var parts []string
	if n := len(res.Removed); n > 0 {
		parts = append(parts, styleOK.Render("✓ ")+fmt.Sprintf("cleaned up %d session(s)", n))
	}
	if n := len(res.Kept); n > 0 {
		parts = append(parts, styleWarn.Render(fmt.Sprintf("kept %d with unsaved work", n)))
	}
	if n := len(res.Failed); n > 0 {
		f := res.Failed[0]
		parts = append(parts, styleFail.Render(fmt.Sprintf("✗ %d failed: %s %s", n, f.Handle, firstLine(f.Err.Error()))))
	}
	if len(parts) == 0 {
		parts = append(parts, styleMuted.Render("no abandoned interactive sessions to clean up"))
	}
	m = m.withToast(strings.Join(parts, styleMuted.Render(" · ")))
	return m.restartStatus()
}

func (m Model) cleanSessionsBox() string {
	leftovers := m.dialog.leftovers
	unsaved := unsavedCount(leftovers)
	width := m.cleanDialogWidth()
	inner := width - styleBox.GetHorizontalFrameSize()
	lines := []string{
		styleBoxHead.Render(fmt.Sprintf("Clean up %d abandoned session(s)?", len(leftovers))),
		"",
		styleMuted.Render(log.Truncate("Removes each worktree and its local branch.", inner)),
		"",
	}
	now := m.deps.Now()
	for i, l := range leftovers {
		if i == cleanDialogRows {
			lines = append(lines, styleMuted.Render(fmt.Sprintf("  … %d more", len(leftovers)-i)))
			break
		}
		lines = append(lines, leftoverRow(l, now, inner))
	}
	if unsaved > 0 {
		options := []string{
			fmt.Sprintf("Clean ones only (keep %d with unsaved work)", unsaved),
			"All, discarding unsaved work",
		}
		lines = append(lines, "")
		for i, o := range options {
			if i == m.dialog.cursor {
				lines = append(lines, styleSelected.Render("› "+o))
			} else {
				lines = append(lines, "  "+o)
			}
		}
	}
	hints := styleKey.Render("⏎") + styleMuted.Render(" clean up   ")
	if unsaved > 0 {
		hints += styleKey.Render("j/k") + styleMuted.Render(" choose   ")
	}
	lines = append(lines, "", hints+styleKey.Render("esc")+styleMuted.Render(" cancel"))
	return styleBox.Width(width).Render(strings.Join(lines, "\n"))
}

func leftoverRow(l session.Leftover, now time.Time, width int) string {
	age := ""
	if started, err := time.Parse(time.RFC3339Nano, l.StartedAt); err == nil {
		age = " · " + log.FormatAge(now.Sub(started))
	}
	var note string
	switch {
	case l.Gone:
		note = styleMuted.Render("worktree already gone")
	case l.Uncommitted && l.Commits:
		note = styleWarn.Render("! changes and commits")
	case l.Uncommitted:
		note = styleWarn.Render("! uncommitted changes")
	case l.Commits:
		note = styleWarn.Render("! unsubmitted commits")
	}
	row := "  " + styleBold.Render(log.PadEnd(log.Truncate(l.Agent, cleanAgentWidth-1), cleanAgentWidth)) + styleMuted.Render(l.Handle+age)
	if note != "" {
		row += "  " + note
	}
	return fit(row, width)
}

func (m Model) cleanDialogWidth() int {
	return max(24, min(cleanDialogWidth, m.width-4))
}
