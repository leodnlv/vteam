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
	focusMinRows     = 3
	focusMaxRows     = 10
	menuLabelWidth   = 20
	cronLabelWidth   = 9
	nextFiresInPopup = 3
)

type dialogKind int

const (
	dialogNone dialogKind = iota
	dialogMenu
	dialogConfirm
	dialogRun
	dialogCron
	dialogStop
)

// dialog is the popup open on the Agents tab: the action menu, the run dialog (dialogConfirm for the
// merge ensurer, which takes no focus), the cron editor, or the stop dialog.
type dialog struct {
	kind   dialogKind
	agent  string
	cursor int
	input  textinput.Model
	save   bool
	focus  textarea.Model
	// interactive opens the run in an interactive Claude session (vteam run --interactive).
	interactive bool
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
			m.dialog = dialog{kind: dialogRun, agent: a.Name, focus: newFocusInput(m.runDialogWidth())}
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
			return m.runOnce(name, "", false), nil
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

// handleRunKey edits the focus text; enter runs the agent with it, shift+enter (or alt+enter, ctrl+j) adds a line.
func (m Model) handleRunKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Cancel):
		m.dialog = dialog{}
		return m, nil
	case key.Matches(msg, keys.Interactive):
		m.dialog.interactive = !m.dialog.interactive
		return m, nil
	case key.Matches(msg, keys.Confirm):
		name, focus, interactive := m.dialog.agent, strings.TrimSpace(m.dialog.focus.Value()), m.dialog.interactive
		m.dialog = dialog{}
		return m.runOnce(name, focus, interactive), nil
	}
	var cmd tea.Cmd
	m.dialog.focus, cmd = m.dialog.focus.Update(msg)
	return m, cmd
}

func (m Model) runOnce(name, focus string, interactive bool) Model {
	if err := m.deps.RunOnce(name, focus, interactive); err != nil {
		return m.withToast(styleFail.Render("✗ " + name + " could not start: " + firstLine(err.Error())))
	}
	m.agents.runIndex, m.agents.follow = -1, true
	text := styleLive.Render("● ") + "started " + styleBold.Render(name)
	if interactive {
		text += styleMuted.Render(" interactively")
	}
	if focus != "" {
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

func (m Model) runBox() string {
	interactiveBox, interactiveHint := styleMuted.Render("[ ]"), "Keeps running after you quit the TUI."
	if m.dialog.interactive {
		interactiveBox = styleOK.Render("[x]")
		interactiveHint = "Opens Claude in a workmux window and switches to it."
	}
	lines := []string{
		styleBoxHead.Render("Run ") + styleBold.Render(m.dialog.agent) + styleBoxHead.Render(" once"),
		"",
		styleMuted.Render("focus") + styleMuted.Render(" (optional, passed as --focus)"),
		m.dialog.focus.View(),
		"",
		interactiveBox + " interactive session" + styleMuted.Render("  tab"),
		styleMuted.Render(interactiveHint),
		"",
		styleKey.Render("⏎") + styleMuted.Render(" run now   ") +
			styleKey.Render("shift+⏎") + styleMuted.Render("/") + styleKey.Render("ctrl+j") + styleMuted.Render(" new line   ") +
			styleKey.Render("esc") + styleMuted.Render(" cancel"),
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
