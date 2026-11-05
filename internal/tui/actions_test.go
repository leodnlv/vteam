package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/lock"
)

// fakeActions records what the TUI asked for and keeps the agent list and scheduler pid in step with it.
type fakeActions struct {
	agents   []Agent
	pid      int
	calls    []string
	runErr   error
	startPID int
}

func (f *fakeActions) setCron(name, expr string) {
	for i := range f.agents {
		if f.agents[i].Name == name {
			f.agents[i].Cron = expr
		}
	}
}

func actionsModel(t *testing.T, f *fakeActions) Model {
	t.Helper()
	m := newTestModel(t, nil)
	m.deps.Agents = func(time.Time) []Agent { return slices.Clone(f.agents) }
	m.deps.SchedulerPID = func() int { return f.pid }
	m.deps.RunOnce = func(name string) error {
		f.calls = append(f.calls, "run "+name)
		return f.runErr
	}
	m.deps.EnableCron = func(name, expr string) error {
		f.calls = append(f.calls, "enable "+name+" "+expr)
		f.setCron(name, expr)
		return nil
	}
	m.deps.DisableCron = func(name string) error {
		f.calls = append(f.calls, "disable "+name)
		f.setCron(name, "")
		return nil
	}
	m.deps.SaveCron = func(name, expr string) error {
		f.calls = append(f.calls, "save "+name+" "+expr)
		return nil
	}
	m.deps.StartScheduler = func() (int, error) {
		f.calls = append(f.calls, "start scheduler")
		f.pid = f.startPID
		return f.startPID, nil
	}
	m = m.refreshAfterAction()
	return press(t, m, "2")
}

func testAgents() []Agent {
	return []Agent{
		{Name: "improver", Config: &config.Agent{Cron: "0 * * * *"}, DefaultCron: "0 * * * *"},
		{Name: "test-writer", Config: &config.Agent{}},
		{Name: "coder", Config: &config.Agent{}, Running: &lock.Info{PID: 7, Timestamp: fixedNow.UnixMilli()}},
	}
}

func assertCalls(t *testing.T, f *fakeActions, want ...string) {
	t.Helper()
	if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls = %q, want %q", f.calls, want)
	}
}

func assertScreen(t *testing.T, m Model, wants ...string) {
	t.Helper()
	out := screen(m)
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("screen is missing %q:\n%s", want, out)
		}
	}
}

func TestEnablingACronFromTheActionMenu(t *testing.T) {
	f := &fakeActions{agents: testAgents(), startPID: 999}
	m := actionsModel(t, f)

	m = press(t, m, "space")
	assertScreen(t, m, "Run once now…", "Run on cron…", "Stop cron", "Open log")
	m = press(t, m, "j", "enter")
	if m.dialog.kind != dialogCron || m.dialog.input.String() != "0 * * * *" {
		t.Fatalf("cron dialog should open prefilled with the agent.json default: %+v", m.dialog)
	}
	assertScreen(t, m, "Run improver on cron", "every hour at :00", "next", "default  0 * * * * (agent.json)")

	m = press(t, m, "ctrl+u", "q", "2", "enter")
	if m.dialog.kind != dialogCron || m.tab != TabAgents {
		t.Fatal("keys typed into the dialog must not quit, switch tabs, or submit an invalid pattern")
	}
	assertScreen(t, m, "✗ expected 5 fields")

	m = press(t, m, "ctrl+u")
	for _, k := range strings.Split("*/30", "") {
		m = press(t, m, k)
	}
	m = press(t, m, "space", "*", "space", "*", "space", "*", "space", "*", "tab", "enter")
	if m.dialog.kind != dialogNone {
		t.Fatal("dialog should close after a valid pattern")
	}
	assertCalls(t, f, "enable improver */30 * * * *", "save improver */30 * * * *", "start scheduler")
	assertScreen(t, m, "◷ improver on cron */30 * * * *", "saved to agent.json", "scheduler started (pid 999)")

	m.toast = toast{}
	assertScreen(t, m, "◷ on cron */30 * * * * · every 30 minutes", "◷ 1 on cron")
}

func TestRunOnceAsksForConfirmation(t *testing.T) {
	f := &fakeActions{agents: testAgents()}
	m := press(t, actionsModel(t, f), "j", "x")
	assertScreen(t, m, "Run test-writer once?", "run now", "cancel")
	m = press(t, m, "esc")
	assertCalls(t, f)

	m = press(t, m, "x", "enter")
	assertCalls(t, f, "run test-writer")
	assertScreen(t, m, "● started test-writer")

	f.runErr = errors.New("no binary")
	m = press(t, m, "x", "enter")
	assertScreen(t, m, "✗ test-writer could not start: no binary")
}

func TestActionsExplainWhyTheyCannotRun(t *testing.T) {
	f := &fakeActions{agents: append(testAgents(), Agent{Name: "retired"})}
	m := press(t, actionsModel(t, f), "j", "j", "x")
	if m.dialog.kind != dialogNone {
		t.Fatal("a running agent should not open the run confirmation")
	}
	assertScreen(t, m, "! coder is already running")
	m = press(t, m, "space")
	assertScreen(t, m, "Run once (running)")
	m = press(t, m, "enter")
	if m.dialog.kind != dialogMenu {
		t.Error("choosing a disabled item should keep the menu open")
	}
	m = press(t, m, "esc", "j", "c")
	assertScreen(t, m, "! retired can't be scheduled")
	m = press(t, m, "s")
	assertScreen(t, m, "! retired has no cron enabled")
	assertCalls(t, f)
}

func TestStoppingACron(t *testing.T) {
	agents := testAgents()
	agents[0].Cron = "0 * * * *"
	f := &fakeActions{agents: agents, pid: 42}
	m := actionsModel(t, f)
	assertScreen(t, m, "◷ on cron 0 * * * *", "cron   0 * * * *")
	m = press(t, m, "s")
	assertCalls(t, f, "disable improver")
	assertScreen(t, m, "improver cron stopped · scheduler stops once idle")
}

func TestPausedCronsCanStartTheScheduler(t *testing.T) {
	agents := testAgents()
	agents[0].Cron = "0 * * * *"
	f := &fakeActions{agents: agents, startPID: 77}
	m := actionsModel(t, f)
	assertScreen(t, m, "◷ cron 0 * * * * paused · scheduler not running", "◷ 1 paused", "0 * * * * paused")
	m = press(t, m, "space")
	assertScreen(t, m, "Start scheduler")
	m = press(t, m, "S")
	assertCalls(t, f, "start scheduler")
	assertScreen(t, m, "scheduler started (pid 77)")
}

func TestEnablingWithARunningSchedulerDoesNotStartAnother(t *testing.T) {
	f := &fakeActions{agents: testAgents(), pid: 5}
	_ = press(t, actionsModel(t, f), "c", "enter")
	assertCalls(t, f, "enable improver 0 * * * *")
}

func TestDescribeCron(t *testing.T) {
	for expr, want := range map[string]string{
		"* * * * *":    "every minute",
		"*/5 * * * *":  "every 5 minutes",
		"0 * * * *":    "every hour at :00",
		"15 */4 * * *": "every 4 hours at :15",
		"30 9 * * *":   "at 09:30 every day",
		"0 9 * * 1-5":  "at 09:00 on weekdays",
		"0 0 1 * *":    "",
	} {
		if got := describeCron(expr); got != want {
			t.Errorf("describeCron(%q) = %q, want %q", expr, got, want)
		}
	}
}

func TestLineInputEditing(t *testing.T) {
	in := newLineInput("0 * * * *")
	for _, k := range []string{"home", "delete", "1", "5", "end", "backspace", "space", "x", "left", "ctrl+w"} {
		in, _ = in.handle(k)
	}
	if got := in.String(); got != "15 * * x" {
		t.Errorf("text = %q", got)
	}
	if _, ok := in.handle("ctrl+k"); ok {
		t.Error("unknown control keys should not be handled")
	}
}
