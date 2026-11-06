package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/tasks"
)

const (
	maxPickerRows = 6
	severityWidth = 9
	// runDialogChrome is the height of the run dialog with a task picker, minus its task rows and "more" lines.
	runDialogChrome = 18
)

// TaskChoice is a task the run dialog offers an input: task agent.
type TaskChoice struct {
	model.Task
	// RetriesExhausted marks a task at the retry limit, which only a run started with --task picks.
	RetriesExhausted bool
}

// LoadTaskChoices lists the ready tasks routed to agent in pick order.
func LoadTaskChoices(ctx context.Context, m tasks.Manager, agent *config.Agent, maxRetries int) ([]TaskChoice, error) {
	ready, err := m.ListReady(ctx)
	if err != nil {
		return nil, err
	}
	var choices []TaskChoice
	for _, t := range tasks.Candidates(ready, agent.TaskFilter) {
		choices = append(choices, TaskChoice{Task: t, RetriesExhausted: t.RetryCount >= maxRetries})
	}
	return choices, nil
}

// taskPicker is the run dialog's task list. Row 0 is Auto, the task the run picks by priority.
type taskPicker struct {
	enabled bool
	// pending means the choices still have to be requested; loading that they have not arrived.
	pending, loading bool
	err              string
	choices          []TaskChoice
	cursor           int
}

type taskChoicesMsg struct {
	agent   string
	choices []TaskChoice
	err     error
}

func newTaskPicker(a Agent) taskPicker {
	if a.Config == nil || a.Config.Input != config.InputTask {
		return taskPicker{}
	}
	return taskPicker{enabled: true, pending: true, loading: true}
}

func (p taskPicker) move(delta int) taskPicker {
	p.cursor = max(0, min(len(p.choices), p.cursor+delta))
	return p
}

// selected is the ref of the chosen task, or "" for Auto.
func (p taskPicker) selected() string {
	if p.cursor == 0 || p.cursor > len(p.choices) {
		return ""
	}
	return p.choices[p.cursor-1].Ref
}

// requestTaskChoices starts loading the run dialog's tasks once it has opened.
func (m Model) requestTaskChoices(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if m.dialog.kind != dialogRun || !m.dialog.picker.pending {
		return m, cmd
	}
	m.dialog.picker.pending = false
	ctx, agent, load := m.ctx, m.dialog.agent, m.deps.TaskChoices
	return m, tea.Batch(cmd, func() tea.Msg {
		choices, err := load(ctx, agent)
		return taskChoicesMsg{agent: agent, choices: choices, err: err}
	})
}

func (m Model) receiveTaskChoices(msg taskChoicesMsg) Model {
	p := &m.dialog.picker
	if m.dialog.kind != dialogRun || m.dialog.agent != msg.agent || !p.loading {
		return m
	}
	p.loading = false
	if msg.err != nil {
		p.err = firstLine(msg.err.Error())
		return m
	}
	p.choices = msg.choices
	return m
}

// pickerLines renders the picker for a dialog whose content is width cells wide, with as many task rows as fit.
func (m Model) pickerLines(width int) []string {
	p := m.dialog.picker
	pickerRows := max(1, min(maxPickerRows, m.bodyHeight()-runDialogChrome))
	header := styleMuted.Render("task")
	if len(p.choices) > 0 {
		header += styleMuted.Render(fmt.Sprintf("  %d ready · ", len(p.choices))) + styleKey.Render("↑/↓") + styleMuted.Render(" choose")
	}
	rows := []string{m.pickerRow(0, p.cursor == 0, autoLabel(p), width)}
	switch {
	case p.loading:
		rows = append(rows, styleMuted.Render("  loading tasks…"))
	case p.err != "":
		rows = append(rows, styleFail.Render(log.Truncate("  ✗ could not list tasks: "+p.err, width)))
	case len(p.choices) == 0:
		rows = append(rows, styleMuted.Render(log.Truncate("  no ready tasks routed to "+m.dialog.agent, width)))
	}
	first := max(0, min(p.cursor-pickerRows+1, len(p.choices)-pickerRows))
	last := min(len(p.choices), first+pickerRows)
	if first > 0 {
		rows = append(rows, styleMuted.Render(fmt.Sprintf("  ↑ %d more", first)))
	}
	for i := first; i < last; i++ {
		rows = append(rows, m.pickerRow(i+1, p.cursor == i+1, "", width))
	}
	if rest := len(p.choices) - last; rest > 0 {
		rows = append(rows, styleMuted.Render(fmt.Sprintf("  ↓ %d more", rest)))
	}
	return append([]string{header}, rows...)
}

func autoLabel(p taskPicker) string {
	label := "Auto · next by priority"
	for _, c := range p.choices {
		if !c.RetriesExhausted {
			return label + " (" + c.Ref + ")"
		}
	}
	if !p.loading && p.err == "" {
		return label + " (nothing eligible)"
	}
	return label
}

// pickerRow draws Auto (row 0) or a task: severity, title, then tags and the ref on the right.
func (m Model) pickerRow(row int, selected bool, auto string, width int) string {
	marker := "  "
	if selected {
		marker = "› "
	}
	if row == 0 {
		text := log.PadEnd(log.Truncate(marker+auto, width), width)
		if selected {
			return styleSelected.Render(text)
		}
		return text
	}
	c := m.dialog.picker.choices[row-1]
	tags := c.Ref
	if c.Metadata[model.MetaPRURL] != "" {
		tags = "PR follow-up · " + tags
	}
	if c.RetriesExhausted {
		tags = fmt.Sprintf("retries %d · %s", c.RetryCount, tags)
	}
	title := log.Truncate(c.Title, max(4, width-len(marker)-severityWidth-lipgloss.Width(tags)-2))
	left := marker + log.PadEnd(string(c.Severity), severityWidth) + title
	gap := max(1, width-lipgloss.Width(left)-lipgloss.Width(tags))
	if selected {
		return styleSelected.Render(log.PadEnd(left+spaces(gap)+tags, width))
	}
	sev := severityStyle(c.Severity).Render(log.PadEnd(string(c.Severity), severityWidth))
	tagStyle := styleMuted
	if c.RetriesExhausted {
		tagStyle = styleWarn
	}
	return marker + sev + title + spaces(gap) + tagStyle.Render(tags)
}

func severityStyle(s model.Severity) lipgloss.Style {
	switch s {
	case model.SeverityCritical:
		return styleFail.Bold(true)
	case model.SeverityHigh:
		return styleFail
	case model.SeverityMedium:
		return styleWarn
	}
	return styleMuted
}

func spaces(n int) string {
	return fmt.Sprintf("%*s", n, "")
}
