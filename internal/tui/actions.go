package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/cron"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/schedule"
)

const (
	toastDuration    = 5 * time.Second
	fallbackCron     = "0 * * * *"
	cronDialogWidth  = 52
	stopDialogWidth  = 56
	runDialogWidth   = 64
	focusMinRows     = 5
	runKeyWidth      = 3
	runLabelWidth    = 8
	runIndentWidth   = runKeyWidth + runLabelWidth
	focusPreviewRows = 3
	focusMaxRows     = 10
	menuLabelWidth   = 20
	cronLabelWidth   = 9
	nextFiresInPopup = 3
)

var runIndent = spaces(runIndentWidth)

type dialogKind int

const (
	dialogNone dialogKind = iota
	dialogMenu
	dialogConfirm
	dialogRun
	dialogCron
	dialogStop
)

// runView is the part of the run dialog on screen: the summary of the run, or the dialog that changes its task or focus.
type runView int

const (
	runSummary runView = iota
	runPickTask
	runEditFocus
)

// dialog is the popup open on the Agents tab: the action menu, the run dialog (dialogConfirm for the
// merge ensurer, which takes no focus), the cron editor, or the stop dialog.
type dialog struct {
	kind   dialogKind
	agent  string
	cursor int
	input  textinput.Model
	save   bool
	// focus is the run dialog's focus editor; focusText is what the run passes as --focus.
	focus     textarea.Model
	focusText string
	runView   runView
	// interactive opens the run in an interactive Claude session (vteam run --interactive).
	interactive bool
	picker      taskPicker
}

type toast struct {
	text  string
	until time.Time
}

type stopOption struct {
	label        string
	keepWorktree bool
}

// stopOptions are the ways to stop a's run; an agent without a worktree has only one.
func stopOptions(a Agent) []stopOption {
	if a.Config == nil || !a.Config.Worktree {
		return []stopOption{{label: "Stop"}}
	}
	return []stopOption{{label: "Stop and remove worktree"}, {label: "Stop and keep worktree", keepWorktree: true}}
}

type menuItem struct {
	key, label string
	enabled    bool
}

func (m Model) agentByName(name string) (Agent, bool) {
	for _, a := range m.agents.list {
		if a.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}

func (m Model) schedulerRunning() bool { return m.info.schedulerPID > 0 }

func (m Model) enabledCrons() int {
	n := 0
	for _, a := range m.agents.list {
		if a.Cron != "" {
			n++
		}
	}
	return n
}

func (m Model) cronsPaused() bool { return m.enabledCrons() > 0 && !m.schedulerRunning() }

func (m Model) menuItems(a Agent) []menuItem {
	run := menuItem{"r", "Run once now…", a.Runnable() && a.Running == nil}
	if a.Running != nil {
		run.label = "Run once (running)"
	}
	stop := menuItem{"x", "Stop run…", a.Running != nil}
	cronItem := menuItem{"c", "Run on cron…", a.Runnable()}
	if a.Cron != "" {
		cronItem.label = "Edit cron…"
	}
	items := []menuItem{run, stop, cronItem, {"s", "Stop cron", a.Cron != ""}}
	if m.cronsPaused() {
		items = append(items, menuItem{"S", "Start scheduler", true})
	}
	return append(items, menuItem{"l", "Open log", true})
}

func (m Model) withToast(text string) Model {
	m.toast = toast{text: text, until: m.deps.Now().Add(toastDuration)}
	return m
}

func (m Model) toastText() string {
	if m.toast.text == "" || !m.deps.Now().Before(m.toast.until) {
		return ""
	}
	return m.toast.text
}

// refreshAfterAction rereads agents and scheduler state so the action shows up without waiting for the next tick.
func (m Model) refreshAfterAction() Model {
	m.agents = m.agents.reload(m.deps.Agents(m.deps.Now()))
	m.info = m.refreshInfo()
	return m
}

// agentAction runs the action bound to key for the selected agent, explaining in a toast when it can't.
func (m Model) agentAction(key string) Model {
	a, ok := m.agents.current()
	if !ok {
		return m
	}
	switch key {
	case "r":
		switch {
		case !a.Runnable():
			return m.withToast(styleWarn.Render("! " + a.Name + " can't be run"))
		case a.Running != nil:
			return m.withToast(styleWarn.Render("! " + a.Name + " is already running"))
		}
		if a.Builtin {
			m.dialog = dialog{kind: dialogConfirm, agent: a.Name}
		} else {
			m.dialog = dialog{kind: dialogRun, agent: a.Name, focus: newFocusInput(m.runDialogWidth()), picker: newTaskPicker(a)}
		}
	case "c":
		if !a.Runnable() {
			return m.withToast(styleWarn.Render("! " + a.Name + " can't be scheduled"))
		}
		expr := a.Cron
		if expr == "" {
			expr = a.DefaultCron
		}
		if expr == "" {
			expr = fallbackCron
		}
		m.dialog = dialog{kind: dialogCron, agent: a.Name, input: newCronInput(expr)}
	case "x":
		if a.Running == nil {
			return m.withToast(styleWarn.Render("! " + a.Name + " is not running"))
		}
		m.dialog = dialog{kind: dialogStop, agent: a.Name}
	case "s":
		if a.Cron == "" {
			return m.withToast(styleWarn.Render("! " + a.Name + " has no cron enabled"))
		}
		return m.stopCron(a.Name)
	case "S":
		if !m.cronsPaused() {
			return m
		}
		return m.startScheduler()
	case "l":
		m.agents.focusLog = true
	case "a", "space":
		m.dialog = dialog{kind: dialogMenu, agent: a.Name}
	}
	return m
}

func (m Model) handleDialogKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch m.dialog.kind {
	case dialogMenu:
		return m.handleMenuKey(msg), nil
	case dialogConfirm:
		switch msg.String() {
		case "enter", "y":
			name := m.dialog.agent
			m.dialog = dialog{}
			return m.runOnce(name, schedule.RunOptions{}), nil
		case "esc", "n", "q":
			m.dialog = dialog{}
		}
	case dialogRun:
		return m.handleRunKey(msg)
	case dialogCron:
		return m.handleCronKey(msg)
	case dialogStop:
		return m.handleStopKey(msg), nil
	}
	return m, nil
}

func (m Model) handleStopKey(msg tea.KeyPressMsg) Model {
	a, ok := m.agentByName(m.dialog.agent)
	if !ok {
		m.dialog = dialog{}
		return m
	}
	options := stopOptions(a)
	switch {
	case key.Matches(msg, keys.Cancel, keys.Quit):
		m.dialog = dialog{}
	case key.Matches(msg, keys.Down):
		m.dialog.cursor = min(len(options)-1, m.dialog.cursor+1)
	case key.Matches(msg, keys.Up):
		m.dialog.cursor = max(0, m.dialog.cursor-1)
	case key.Matches(msg, keys.Confirm):
		keep := options[min(m.dialog.cursor, len(options)-1)].keepWorktree
		m.dialog = dialog{}
		return m.stopRun(a.Name, keep)
	}
	return m
}

func (m Model) handleMenuKey(msg tea.KeyPressMsg) Model {
	a, ok := m.agentByName(m.dialog.agent)
	if !ok {
		m.dialog = dialog{}
		return m
	}
	items := m.menuItems(a)
	pressed := msg.String()
	switch {
	case key.Matches(msg, keys.Cancel, keys.Quit, keys.Actions):
		m.dialog = dialog{}
		return m
	case key.Matches(msg, keys.Down):
		m.dialog.cursor = min(len(items)-1, m.dialog.cursor+1)
		return m
	case key.Matches(msg, keys.Up):
		m.dialog.cursor = max(0, m.dialog.cursor-1)
		return m
	case key.Matches(msg, keys.Confirm):
		pressed = items[min(m.dialog.cursor, len(items)-1)].key
	}
	for _, it := range items {
		if it.key == pressed && it.enabled {
			m.dialog = dialog{}
			return m.agentAction(pressed)
		}
	}
	return m
}

func (m Model) handleCronKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Cancel):
		m.dialog = dialog{}
	case key.Matches(msg, keys.SaveDefault):
		m.dialog.save = !m.dialog.save
	case key.Matches(msg, keys.Confirm):
		expr := cronExpr(m.dialog.input)
		if _, err := cron.Parse(expr); err != nil {
			return m, nil
		}
		d := m.dialog
		m.dialog = dialog{}
		return m.enableCron(d.agent, expr, d.save), nil
	default:
		var cmd tea.Cmd
		m.dialog.input, cmd = m.dialog.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func newCronInput(expr string) textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	styles := textinput.DefaultDarkStyles()
	styles.Focused.Text = lipgloss.NewStyle()
	styles.Cursor.Blink = false
	in.SetStyles(styles)
	in.SetWidth(cronDialogWidth - styleBox.GetHorizontalFrameSize() - cronLabelWidth - 1)
	in.SetValue(expr)
	in.Focus()
	return in
}

// cronExpr is the typed pattern with runs of spaces collapsed.
func cronExpr(in textinput.Model) string {
	return strings.Join(strings.Fields(in.Value()), " ")
}

func newFocusInput(width int) textarea.Model {
	in := textarea.New()
	in.ShowLineNumbers = false
	in.Prompt = "┃ "
	in.Placeholder = "What should the agent focus on?"
	in.DynamicHeight = true
	in.MinHeight = focusMinRows
	in.MaxHeight = focusMaxRows
	in.KeyMap.InsertNewline = keys.NewLine
	styles := textarea.DefaultDarkStyles()
	styles.Focused.CursorLine = lipgloss.NewStyle()
	styles.Focused.Prompt = styleMuted
	styles.Cursor.Blink = false
	in.SetStyles(styles)
	in.SetWidth(width - styleBox.GetHorizontalFrameSize())
	in.Focus()
	return in
}

func (m Model) runDialogWidth() int {
	return max(24, min(runDialogWidth, m.width-4))
}

// handleRunKey handles the run dialog's summary: each part of the run has its own key, and enter runs the agent.
func (m Model) handleRunKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch m.dialog.runView {
	case runPickTask:
		return m.handlePickTaskKey(msg)
	case runEditFocus:
		return m.handleEditFocusKey(msg)
	}
	switch {
	case key.Matches(msg, keys.Cancel, keys.Quit):
		m.dialog = dialog{}
	case key.Matches(msg, keys.Interactive):
		m.dialog.interactive = !m.dialog.interactive
	case m.dialog.picker.enabled && key.Matches(msg, keys.PickTask):
		m.dialog.picker = m.dialog.picker.open(m.pickerDialogWidth() - styleBox.GetHorizontalFrameSize() - 20)
		m.dialog.runView = runPickTask
	case key.Matches(msg, keys.EditFocus):
		m.dialog.focus.SetValue(m.dialog.focusText)
		m.dialog.focus.MoveToEnd()
		m.dialog.runView = runEditFocus
	case key.Matches(msg, keys.Confirm):
		name := m.dialog.agent
		opts := schedule.RunOptions{
			Focus: strings.TrimSpace(m.dialog.focusText), Task: m.dialog.picker.chosen, Interactive: m.dialog.interactive,
		}
		m.dialog = dialog{}
		return m.runOnce(name, opts), nil
	}
	return m, nil
}

// handlePickTaskKey filters and moves the task dialog's list; enter chooses the highlighted row, esc keeps the old choice.
func (m Model) handlePickTaskKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	p := &m.dialog.picker
	switch {
	case key.Matches(msg, keys.Cancel):
		m.dialog.runView = runSummary
	case key.Matches(msg, keys.Confirm):
		p.chosen = p.highlighted()
		m.dialog.runView = runSummary
	case key.Matches(msg, keys.TaskDown):
		*p = p.move(1)
	case key.Matches(msg, keys.TaskUp):
		*p = p.move(-1)
	default:
		var cmd tea.Cmd
		*p, cmd = p.updateFilter(msg)
		return m, cmd
	}
	return m, nil
}

// handleEditFocusKey edits the focus text; enter saves it, shift+enter (or alt+enter, ctrl+j) adds a line, esc discards.
func (m Model) handleEditFocusKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Cancel):
		m.dialog.runView = runSummary
	case key.Matches(msg, keys.Confirm):
		m.dialog.focusText = m.dialog.focus.Value()
		m.dialog.runView = runSummary
	default:
		var cmd tea.Cmd
		m.dialog.focus, cmd = m.dialog.focus.Update(msg)
		return m, cmd
	}
	return m, nil
}

// pasteIntoDialog sends pasted text to the dialog's text field on screen, if it has one.
func (m Model) pasteIntoDialog(msg tea.PasteMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	switch {
	case m.dialog.kind == dialogRun && m.dialog.runView == runEditFocus:
		m.dialog.focus, cmd = m.dialog.focus.Update(msg)
	case m.dialog.kind == dialogRun && m.dialog.runView == runPickTask:
		m.dialog.picker, cmd = m.dialog.picker.updateFilter(msg)
	case m.dialog.kind == dialogCron:
		m.dialog.input, cmd = m.dialog.input.Update(msg)
	}
	return m, cmd
}

func (m Model) runOnce(name string, opts schedule.RunOptions) Model {
	if err := m.deps.RunOnce(name, opts); err != nil {
		return m.withToast(styleFail.Render("✗ " + name + " could not start: " + firstLine(err.Error())))
	}
	m.agents.runIndex, m.agents.follow = -1, true
	text := styleLive.Render("● ") + "started " + styleBold.Render(name)
	if opts.Task != "" {
		text += " on " + styleBold.Render(opts.Task)
	}
	if opts.Interactive {
		text += styleMuted.Render(" interactively")
	}
	if opts.Focus != "" {
		text += styleMuted.Render(" with focus")
	}
	return m.refreshAfterAction().withToast(text)
}

func (m Model) stopRun(name string, keepWorktree bool) Model {
	if err := m.deps.StopRun(name, keepWorktree); err != nil {
		return m.withToast(styleFail.Render("✗ " + firstLine(err.Error())))
	}
	text := styleWarn.Render("■ ") + "stopping " + styleBold.Render(name)
	if keepWorktree {
		text += styleMuted.Render(" · keeping its worktree")
	}
	return m.refreshAfterAction().withToast(text)
}

func (m Model) enableCron(name, expr string, save bool) Model {
	if err := m.deps.EnableCron(name, expr); err != nil {
		return m.withToast(styleFail.Render("✗ " + firstLine(err.Error())))
	}
	parts := []string{styleCron.Render("◷ ") + styleBold.Render(name) + " on cron " + expr}
	if save {
		if err := m.deps.SaveCron(name, expr); err != nil {
			parts = append(parts, styleFail.Render("saving default failed: "+firstLine(err.Error())))
		} else {
			parts = append(parts, "saved to "+schedule.DefaultCronFile(name))
		}
	}
	if m.deps.SchedulerPID() == 0 {
		if pid, err := m.deps.StartScheduler(); err != nil {
			parts = append(parts, styleFail.Render("scheduler failed to start: "+firstLine(err.Error())))
		} else {
			parts = append(parts, fmt.Sprintf("scheduler started (pid %d)", pid))
		}
	}
	return m.refreshAfterAction().withToast(strings.Join(parts, styleMuted.Render(" · ")))
}

func (m Model) stopCron(name string) Model {
	if err := m.deps.DisableCron(name); err != nil {
		return m.withToast(styleFail.Render("✗ " + firstLine(err.Error())))
	}
	m = m.refreshAfterAction()
	text := styleMuted.Render("○ ") + styleBold.Render(name) + " cron stopped"
	if m.enabledCrons() == 0 && m.schedulerRunning() {
		text += styleMuted.Render(" · scheduler stops once idle")
	}
	return m.withToast(text)
}

func (m Model) startScheduler() Model {
	pid, err := m.deps.StartScheduler()
	if err != nil {
		return m.withToast(styleFail.Render("✗ scheduler failed to start: " + firstLine(err.Error())))
	}
	return m.refreshAfterAction().withToast(styleOK.Render("✓ ") + fmt.Sprintf("scheduler started (pid %d)", pid))
}

// renderDialog draws the open dialog over the Agents tab body.
func (m Model) renderDialog(body []string) []string {
	height := m.bodyHeight()
	switch m.dialog.kind {
	case dialogMenu:
		box := m.menuBox()
		y := min(m.agents.selected+2, max(0, height-lipgloss.Height(box)))
		return overlayAt(body, box, sidebarWidth-6, y, m.width, height)
	case dialogConfirm:
		return overlay(body, m.confirmBox(), m.width, height)
	case dialogRun:
		switch m.dialog.runView {
		case runPickTask:
			return overlay(body, m.pickerBox(), m.width, height)
		case runEditFocus:
			return overlay(body, m.focusBox(), m.width, height)
		}
		return overlay(body, m.runBox(), m.width, height)
	case dialogCron:
		return overlay(body, m.cronBox(), m.width, height)
	case dialogStop:
		return overlay(body, m.stopBox(), m.width, height)
	}
	return body
}

func (m Model) menuBox() string {
	a, _ := m.agentByName(m.dialog.agent)
	lines := []string{styleBoxHead.Render(a.Name)}
	for i, it := range m.menuItems(a) {
		row := log.PadEnd(it.label, menuLabelWidth) + " " + it.key
		switch {
		case i == m.dialog.cursor:
			lines = append(lines, styleSelected.Render(row))
		case !it.enabled:
			lines = append(lines, styleOff.Render(row))
		default:
			lines = append(lines, log.PadEnd(it.label, menuLabelWidth)+" "+styleKey.Render(it.key))
		}
	}
	return styleBox.Render(strings.Join(lines, "\n"))
}

func (m Model) confirmBox() string {
	lines := []string{
		styleBoxHead.Render("Run ") + styleBold.Render(m.dialog.agent) + styleBoxHead.Render(" once?"),
		"",
		styleKey.Render("⏎") + styleMuted.Render(" run now   ") + styleKey.Render("esc") + styleMuted.Render(" cancel"),
	}
	return styleBox.Render(strings.Join(lines, "\n"))
}

func (m Model) stopBox() string {
	a, _ := m.agentByName(m.dialog.agent)
	lines := []string{styleBoxHead.Render("Stop ") + styleBold.Render(a.Name) + styleBoxHead.Render("?"), ""}
	switch {
	case a.Builtin:
		lines = append(lines, styleMuted.Render("It finishes the PR it is on, then stops."))
	case a.Config != nil:
		text := "Claude is stopped, and the on-finish hook and memory curation are skipped. Output Claude already returned is still pushed."
		if a.Config.Input == config.InputTask {
			text += " Its task goes back to todo."
		}
		lines = append(lines, styleMuted.Render(text))
	}
	options := stopOptions(a)
	if len(options) > 1 {
		lines = append(lines, "")
		for i, o := range options {
			if i == m.dialog.cursor {
				lines = append(lines, styleSelected.Render("› "+o.label))
			} else {
				lines = append(lines, "  "+o.label)
			}
		}
		lines = append(lines, styleMuted.Render("  a kept worktree stays until `vteam clean`"))
	}
	hints := styleKey.Render("⏎") + styleMuted.Render(" stop   ")
	if len(options) > 1 {
		hints += styleKey.Render("j/k") + styleMuted.Render(" choose   ")
	}
	lines = append(lines, "", hints+styleKey.Render("esc")+styleMuted.Render(" cancel"))
	return styleBox.Width(stopDialogWidth).Render(strings.Join(lines, "\n"))
}

// runBox is the run dialog's summary: one row per part of the run, each led by the key that changes it.
func (m Model) runBox() string {
	width := m.runDialogWidth() - styleBox.GetHorizontalFrameSize()
	lines := []string{m.runTitle(""), ""}
	if m.dialog.picker.enabled {
		lines = append(append(lines, m.taskSummaryLines(width)...), "")
	}
	lines = append(append(lines, m.focusSummaryLines(width)...), "")
	mode, hint := styleBold.Render("headless"), "Keeps running after you quit the TUI."
	if m.dialog.interactive {
		mode, hint = styleFinishedOK.Render("interactive"), "Opens Claude in a workmux window."
	}
	lines = append(lines,
		runRow(keys.Interactive, "mode", mode),
		runIndent+styleMuted.Render(log.Truncate(hint, width-runIndentWidth)),
		"",
	)
	hints := styleKey.Render("⏎") + styleMuted.Render(" run   ")
	if m.dialog.picker.enabled {
		hints += styleKey.Render("t") + styleMuted.Render(" task   ")
	}
	hints += styleKey.Render("f") + styleMuted.Render(" focus   ") + styleKey.Render("i") + styleMuted.Render(" interactive   ") +
		styleKey.Render("esc") + styleMuted.Render(" cancel")
	lines = append(lines, hints)
	return styleBox.Width(m.runDialogWidth()).Render(strings.Join(lines, "\n"))
}

// runTitle is the run dialog's heading, followed by the sub-dialog's name when one is open.
func (m Model) runTitle(sub string) string {
	title := styleBoxHead.Render("Run ") + styleBold.Render(m.dialog.agent) + styleBoxHead.Render(" once")
	if sub != "" {
		title += styleMuted.Render(" › " + sub)
	}
	return title
}

// runRow is a row of the run dialog's summary: the key, the label, then the value.
func runRow(b key.Binding, label, value string) string {
	return styleKey.Render(log.PadEnd(b.Help().Key, runKeyWidth)) + styleMuted.Render(log.PadEnd(label, runLabelWidth)) + value
}

// focusSummaryLines show the first lines of the focus text, or "none".
func (m Model) focusSummaryLines(width int) []string {
	text := strings.TrimSpace(m.dialog.focusText)
	if text == "" {
		return []string{runRow(keys.EditFocus, "focus", styleMuted.Render("none"))}
	}
	textWidth := width - runIndentWidth - 2
	wrapped := strings.Split(xansi.Wrap(text, textWidth, ""), "\n")
	if len(wrapped) > focusPreviewRows {
		wrapped = append(wrapped[:focusPreviewRows-1], log.Truncate(wrapped[focusPreviewRows-1], textWidth-2)+" …")
	}
	lines := make([]string, len(wrapped))
	for i, l := range wrapped {
		lines[i] = runIndent + styleMuted.Render("┃ ") + l
	}
	lines[0] = runRow(keys.EditFocus, "focus", styleMuted.Render("┃ ")+wrapped[0])
	return lines
}

// focusBox is the run dialog's focus editor.
func (m Model) focusBox() string {
	width := m.runDialogWidth() - styleBox.GetHorizontalFrameSize()
	about := xansi.Wrap("Passed as --focus, a priority section in the prompt.\nLeave it empty for a normal run.", width, "")
	lines := []string{
		m.runTitle("focus"),
		"",
		styleMuted.Render(about),
		"",
		m.dialog.focus.View(),
		"",
		styleKey.Render("⏎") + styleMuted.Render(" save   ") +
			styleKey.Render("shift+⏎") + styleMuted.Render("/") + styleKey.Render("ctrl+j") + styleMuted.Render(" new line   ") +
			styleKey.Render("esc") + styleMuted.Render(" discard"),
	}
	return styleBox.Width(m.runDialogWidth()).Render(strings.Join(lines, "\n"))
}

func (m Model) cronBox() string {
	d := m.dialog
	a, _ := m.agentByName(d.agent)
	title := "Run " + d.agent + " on cron"
	verb := "start"
	if a.Cron != "" {
		title, verb = "Edit cron of "+d.agent, "update"
	}
	label := func(s string) string { return styleMuted.Render(log.PadEnd(s, cronLabelWidth)) }
	lines := []string{styleBoxHead.Render(title), "", label("cron") + d.input.View()}

	now := m.deps.Now()
	expr := cronExpr(d.input)
	if s, err := cron.Parse(expr); err != nil {
		lines = append(lines, label("")+styleFail.Render("✗ "+err.Error()), "")
	} else {
		lines = append(lines, label("")+styleMuted.Render(orText(describeCron(expr), "custom schedule")))
		var fires []string
		t := now
		for range nextFiresInPopup {
			if t = s.Next(t); t.IsZero() {
				break
			}
			fires = append(fires, log.FormatTimeOfDay(t.Local(), now.Local()))
		}
		lines = append(lines, label("next")+styleCron.Render(strings.Join(fires, " · ")))
	}

	file := schedule.DefaultCronFile(d.agent)
	if a.DefaultCron != "" {
		lines = append(lines, label("default")+a.DefaultCron+styleMuted.Render(" ("+file+")"))
	} else {
		lines = append(lines, label("default")+styleMuted.Render("none in "+file))
	}
	box := styleMuted.Render("[ ]")
	if d.save {
		box = styleOK.Render("[x]")
	}
	lines = append(lines, "",
		box+" save as default in "+file+styleMuted.Render("  tab"),
		styleMuted.Render("Keeps running after you quit the TUI."),
		"",
		styleKey.Render("⏎")+styleMuted.Render(" "+verb+"   ")+styleKey.Render("esc")+styleMuted.Render(" cancel"),
	)
	return styleBox.Width(cronDialogWidth).Render(strings.Join(lines, "\n"))
}

func orText(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

var (
	cronNumber = regexp.MustCompile(`^\d+$`)
	cronStep   = regexp.MustCompile(`^\*/(\d+)$`)
)

// describeCron names the common cron shapes in words; it returns "" for anything else.
func describeCron(expr string) string {
	f := strings.Fields(expr)
	if len(f) != 5 || f[2] != "*" || f[3] != "*" {
		return ""
	}
	minute, hour, dow := f[0], f[1], f[4]
	days := ""
	switch dow {
	case "*":
	case "1-5", "mon-fri", "MON-FRI":
		days = " on weekdays"
	default:
		days = " on weekdays " + dow
	}
	pad := func(s string) string {
		if len(s) == 1 {
			return "0" + s
		}
		return s
	}
	switch {
	case minute == "*" && hour == "*":
		return "every minute" + days
	case cronStep.MatchString(minute) && hour == "*":
		return "every " + cronStep.FindStringSubmatch(minute)[1] + " minutes" + days
	case cronNumber.MatchString(minute) && hour == "*":
		return "every hour at :" + pad(minute) + days
	case cronNumber.MatchString(minute) && cronStep.MatchString(hour):
		return "every " + cronStep.FindStringSubmatch(hour)[1] + " hours at :" + pad(minute) + days
	case cronNumber.MatchString(minute) && cronNumber.MatchString(hour):
		if days == "" {
			days = " every day"
		}
		return "at " + pad(hour) + ":" + pad(minute) + days
	}
	return ""
}
