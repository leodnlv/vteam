package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/tasks"
)

const (
	maxPickerRows     = 8
	severityWidth     = 9
	pickerDialogWidth = 78
	detailRows        = 3
	// pickerDialogChrome is the height of the task dialog minus its task rows and "more" lines.
	pickerDialogChrome = 17
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

// taskPicker is the run dialog's task choice and the task dialog that changes it. In the dialog's list, row 0 is
// Auto, the task the run picks by priority, and the rows after it are the choices matching the filter.
type taskPicker struct {
	enabled bool
	// pending means the choices still have to be requested; loading that they have not arrived.
	pending, loading bool
	err              string
	choices          []TaskChoice
	// chosen is the ref the run passes as --task, "" for Auto.
	chosen string
	cursor int
	filter textinput.Model
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

// open prepares the task dialog: an empty filter, with the current choice highlighted.
func (p taskPicker) open(width int) taskPicker {
	p.filter = newFilterInput(width)
	p.cursor = 0
	for i, c := range p.choices {
		if c.Ref == p.chosen {
			p.cursor = i + 1
		}
	}
	return p
}

func newFilterInput(width int) textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "type to filter"
	styles := textinput.DefaultDarkStyles()
	styles.Focused.Text = lipgloss.NewStyle()
	styles.Cursor.Blink = false
	in.SetStyles(styles)
	in.SetWidth(width)
	in.Focus()
	return in
}

// visible are the choices whose title, ref, or found-by contains the filter text.
func (p taskPicker) visible() []TaskChoice {
	query := strings.ToLower(strings.TrimSpace(p.filter.Value()))
	if query == "" {
		return p.choices
	}
	var out []TaskChoice
	for _, c := range p.choices {
		if strings.Contains(strings.ToLower(c.Title+" "+c.Ref+" "+c.Metadata[model.MetaFoundBy]), query) {
			out = append(out, c)
		}
	}
	return out
}

func (p taskPicker) move(delta int) taskPicker {
	p.cursor = max(0, min(len(p.visible()), p.cursor+delta))
	return p
}

// updateFilter passes msg to the filter and, when the text changed, highlights the first match.
func (p taskPicker) updateFilter(msg tea.Msg) (taskPicker, tea.Cmd) {
	before := p.filter.Value()
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(msg)
	if p.filter.Value() != before {
		p.cursor = min(1, len(p.visible()))
	}
	return p, cmd
}

// highlighted is the ref of the task dialog's highlighted row, or "" for Auto.
func (p taskPicker) highlighted() string {
	visible := p.visible()
	if p.cursor == 0 || p.cursor > len(visible) {
		return ""
	}
	return visible[p.cursor-1].Ref
}

func (p taskPicker) choice(ref string) (TaskChoice, bool) {
	for _, c := range p.choices {
		if c.Ref == ref {
			return c, true
		}
	}
	return TaskChoice{}, false
}

// auto is the task a run without --task would take: the first one under the retry limit.
func (p taskPicker) auto() (TaskChoice, bool) {
	for _, c := range p.choices {
		if !c.RetriesExhausted {
			return c, true
		}
	}
	return TaskChoice{}, false
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

func (m Model) pickerDialogWidth() int {
	return max(24, min(pickerDialogWidth, m.width-4))
}

// taskSummaryLines are the task row of the run dialog and the line under it describing the task the run takes.
func (m Model) taskSummaryLines(width int) []string {
	p := m.dialog.picker
	head := styleBold.Render("Auto") + styleMuted.Render(" · next by priority")
	if p.chosen != "" {
		head = styleBold.Render(p.chosen) + styleMuted.Render(" picked")
	}
	first := runRow(keys.PickTask, "task", head)
	detailWidth := width - runIndentWidth
	if c, ok := p.choice(p.chosen); ok {
		return []string{first, runIndent + choiceLine(c, detailWidth, false)}
	}
	var detail string
	auto, ok := p.auto()
	switch {
	case p.loading:
		detail = styleMuted.Render("loading tasks…")
	case p.err != "":
		detail = styleFail.Render(log.Truncate("✗ could not list tasks: "+p.err, detailWidth))
	case len(p.choices) == 0:
		detail = styleMuted.Render(log.Truncate("no ready tasks routed to "+m.dialog.agent, detailWidth))
	case !ok:
		detail = styleWarn.Render(log.Truncate("nothing eligible, all at the retry limit", detailWidth))
	default:
		detail = choiceLine(auto, detailWidth, true)
	}
	return []string{first, runIndent + detail}
}

// choiceLine is a task in the run dialog: severity, title, then its tags (and its ref, when withRef) on the right.
func choiceLine(c TaskChoice, width int, withRef bool) string {
	var tags []string
	if c.RetriesExhausted {
		tags = append(tags, fmt.Sprintf("retries %d", c.RetryCount))
	}
	if c.Metadata[model.MetaPRURL] != "" {
		tags = append(tags, "PR follow-up")
	}
	if withRef {
		tags = append(tags, c.Ref)
	}
	tagText := strings.Join(tags, " · ")
	sevWidth := severityWidth - 1
	title := log.Truncate(c.Title, max(4, width-sevWidth-lipgloss.Width(tagText)-2))
	gap := max(1, width-sevWidth-lipgloss.Width(title)-lipgloss.Width(tagText))
	tagStyle := styleMuted
	if c.RetriesExhausted {
		tagStyle = styleWarn
	}
	return severityStyle(c.Severity).Render(log.PadEnd(string(c.Severity), sevWidth)) + title + spaces(gap) + tagStyle.Render(tagText)
}

// pickerBox is the task dialog: the filter, the matching tasks with as many rows as fit, and the highlighted task's details.
func (m Model) pickerBox() string {
	width := m.pickerDialogWidth()
	inner := width - styleBox.GetHorizontalFrameSize()
	p := m.dialog.picker
	visible := p.visible()
	count := styleMuted.Render(fmt.Sprintf("%d of %d ready", len(visible), len(p.choices)))
	filter := styleKey.Render("/ ") + p.filter.View()
	lines := []string{
		m.runTitle("choose task"),
		"",
		filter + spaces(max(1, inner-lipgloss.Width(filter)-lipgloss.Width(count))) + count,
		"",
	}
	lines = append(lines, m.pickerRows(visible, inner)...)
	lines = append(lines, styleRule.Render(strings.Repeat("─", inner)))
	lines = append(lines, m.pickerDetail(inner)...)
	lines = append(lines, "",
		styleKey.Render("⏎")+styleMuted.Render(" choose   ")+styleKey.Render("↑/↓")+styleMuted.Render(" move   ")+
			styleKey.Render("esc")+styleMuted.Render(" back"))
	return styleBox.Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) pickerRows(visible []TaskChoice, width int) []string {
	p := m.dialog.picker
	rowCount := max(1, min(maxPickerRows, m.bodyHeight()-pickerDialogChrome))
	rows := []string{pickerRow(p.cursor == 0, autoLabel(p), TaskChoice{}, width)}
	switch {
	case p.loading:
		rows = append(rows, styleMuted.Render("  loading tasks…"))
	case p.err != "":
		rows = append(rows, styleFail.Render(log.Truncate("  ✗ could not list tasks: "+p.err, width)))
	case len(p.choices) == 0:
		rows = append(rows, styleMuted.Render(log.Truncate("  no ready tasks routed to "+m.dialog.agent, width)))
	case len(visible) == 0:
		rows = append(rows, styleMuted.Render("  no tasks match"))
	}
	first := max(0, min(p.cursor-rowCount, len(visible)-rowCount))
	last := min(len(visible), first+rowCount)
	if first > 0 {
		rows = append(rows, styleMuted.Render(fmt.Sprintf("  ↑ %d more", first)))
	}
	for i := first; i < last; i++ {
		rows = append(rows, pickerRow(p.cursor == i+1, "", visible[i], width))
	}
	if rest := len(visible) - last; rest > 0 {
		rows = append(rows, styleMuted.Render(fmt.Sprintf("  ↓ %d more", rest)))
	}
	return rows
}

// pickerDetail describes the highlighted row in a fixed number of lines, so the dialog keeps its height.
func (m Model) pickerDetail(width int) []string {
	p := m.dialog.picker
	var head, body string
	if ref := p.highlighted(); ref == "" {
		head = styleBold.Render("Auto") + styleMuted.Render(" takes the next task by priority when the run starts.")
		if auto, ok := p.auto(); ok {
			body = "Right now that is " + auto.Ref + ": " + auto.Title
		}
	} else {
		c, _ := p.choice(ref)
		parts := []string{c.Ref}
		if by := c.Metadata[model.MetaFoundBy]; by != "" {
			parts = append(parts, "found by "+by)
		}
		if c.Metadata[model.MetaPRURL] != "" {
			parts = append(parts, "PR follow-up")
		}
		head = severityStyle(c.Severity).Render(string(c.Severity)) + styleMuted.Render(" · "+strings.Join(parts, " · "))
		if c.RetriesExhausted {
			head += styleWarn.Render(fmt.Sprintf(" · retries %d, only a picked run takes it", c.RetryCount))
		}
		body = c.Description
	}
	lines := []string{xansi.Truncate(head, width, "…")}
	wrapped := strings.Split(xansi.Wrap(strings.Join(strings.Fields(body), " "), width, ""), "\n")
	if len(wrapped) > detailRows {
		wrapped = append(wrapped[:detailRows-1], log.Truncate(wrapped[detailRows-1]+" …", width))
	}
	for i := range detailRows {
		line := ""
		if i < len(wrapped) {
			line = styleMuted.Render(wrapped[i])
		}
		lines = append(lines, line)
	}
	return lines
}

func autoLabel(p taskPicker) string {
	label := "Auto · next by priority"
	if auto, ok := p.auto(); ok {
		return label + " (" + auto.Ref + ")"
	}
	if !p.loading && p.err == "" {
		return label + " (nothing eligible)"
	}
	return label
}

// pickerRow draws Auto (when auto is set) or a task: severity, title, then tags and the ref on the right.
func pickerRow(selected bool, auto string, c TaskChoice, width int) string {
	marker := "  "
	if selected {
		marker = "› "
	}
	if auto != "" {
		text := log.PadEnd(log.Truncate(marker+auto, width), width)
		if selected {
			return styleSelected.Render(text)
		}
		return text
	}
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
