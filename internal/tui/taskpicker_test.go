package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/tasks/taskstest"
)

func taskAgents() []Agent {
	return []Agent{{Name: "coder", Config: &config.Agent{Input: config.InputTask}}}
}

func testChoices() []TaskChoice {
	return []TaskChoice{
		{Task: model.Task{Ref: "APP-SPNT", Title: "Retry storm", Severity: "critical", RetryCount: 3}, RetriesExhausted: true},
		{Task: model.Task{Ref: "APP-HIGH", Title: "Fix the parser", Severity: "high", Metadata: map[string]string{model.MetaPRURL: "https://x/pr/1"}}},
		{Task: model.Task{Ref: "APP-LOW1", Title: "Rename a helper", Severity: "low"}},
	}
}

// openTaskRunDialog presses r on the selected agent and delivers the task choices the dialog asks for.
func openTaskRunDialog(t *testing.T, m Model, choices []TaskChoice, err error) Model {
	t.Helper()
	var asked []string
	m.deps.TaskChoices = func(_ context.Context, agent string) ([]TaskChoice, error) {
		asked = append(asked, agent)
		return choices, err
	}
	next, cmd := m.Update(keyPress("r"))
	m = next.(Model)
	assertScreen(t, m, "loading tasks…")
	for _, msg := range runCmd(cmd) {
		m = update(t, m, msg)
	}
	if !slices.Equal(asked, []string{"coder"}) {
		t.Fatalf("task choices asked for %q", asked)
	}
	return m
}

func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var msgs []tea.Msg
	for _, c := range batch {
		msgs = append(msgs, runCmd(c)...)
	}
	return msgs
}

func tallActionsModel(t *testing.T, f *fakeActions) Model {
	t.Helper()
	return update(t, actionsModel(t, f), tea.WindowSizeMsg{Width: 100, Height: 40})
}

func TestRunDialogPicksATask(t *testing.T) {
	f := &fakeActions{agents: taskAgents()}
	m := openTaskRunDialog(t, tallActionsModel(t, f), testChoices(), nil)
	assertScreen(t, m, "3 ready", "Auto · next by priority (APP-HIGH)", "critical", "Retry storm", "retries 3 · APP-SPNT",
		"Fix the parser", "PR follow-up · APP-HIGH", "Rename a helper")

	m = update(t, press(t, m, "down", "down", "down", "down", "up"), keyEnter)
	assertCalls(t, f, "run coder --task APP-HIGH")
	assertScreen(t, m, "● started coder on APP-HIGH")
}

func TestRunDialogDefaultsToAuto(t *testing.T) {
	f := &fakeActions{agents: taskAgents()}
	m := openTaskRunDialog(t, tallActionsModel(t, f), testChoices(), nil)
	m = typeText(t, m, "jk")
	update(t, press(t, m, "down", "up", "up"), keyEnter)
	assertCalls(t, f, "run coder --focus jk")
}

func TestRunDialogStillRunsWhenTasksCannotBeListed(t *testing.T) {
	f := &fakeActions{agents: taskAgents()}
	m := openTaskRunDialog(t, tallActionsModel(t, f), nil, errors.New("aven: not found"))
	assertScreen(t, m, "✗ could not list tasks: aven: not found", "Auto · next by priority")
	update(t, press(t, m, "down"), keyEnter)
	assertCalls(t, f, "run coder")
}

func TestRunDialogSaysWhenNoTaskIsRouted(t *testing.T) {
	f := &fakeActions{agents: taskAgents()}
	m := openTaskRunDialog(t, tallActionsModel(t, f), nil, nil)
	assertScreen(t, m, "Auto · next by priority (nothing eligible)", "no ready tasks routed to coder")
}

func TestRunDialogScrollsLongTaskLists(t *testing.T) {
	var choices []TaskChoice
	for _, ref := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		choices = append(choices, TaskChoice{Task: model.Task{Ref: "APP-" + ref, Title: "task " + ref, Severity: "low"}})
	}
	f := &fakeActions{agents: taskAgents()}
	m := openTaskRunDialog(t, tallActionsModel(t, f), choices, nil)
	assertScreen(t, m, "↓ 2 more")
	m = press(t, m, "down", "down", "down", "down", "down", "down", "down", "down")
	assertScreen(t, m, "↑ 2 more", "task H")
	if strings.Contains(screen(m), "task A") {
		t.Error("the first task should have scrolled out of view")
	}
}

func TestRunDialogHasNoPickerForAgentsWithoutTaskInput(t *testing.T) {
	f := &fakeActions{agents: testAgents()}
	m := actionsModel(t, f)
	m.deps.TaskChoices = func(context.Context, string) ([]TaskChoice, error) {
		t.Fatal("task choices asked for an agent without input: task")
		return nil, nil
	}
	next, cmd := press(t, m, "j").Update(keyPress("r"))
	runCmd(cmd)
	if strings.Contains(screen(next.(Model)), "Auto") {
		t.Error("the run dialog shows a task picker")
	}
}

func TestLoadTaskChoicesMarksTasksAtTheRetryLimit(t *testing.T) {
	fake := taskstest.New(
		model.Task{Ref: "APP-LOW1", Status: model.StatusTodo, Severity: "low"},
		model.Task{Ref: "APP-SPNT", Status: model.StatusTodo, Severity: "high", RetryCount: 2},
		model.Task{Ref: "APP-OTHR", Status: model.StatusTodo, Severity: "critical", Metadata: map[string]string{"actor": "x"}},
	)
	agent := &config.Agent{Input: config.InputTask, TaskFilter: config.TaskFilter{"actor": {""}}}
	choices, err := LoadTaskChoices(context.Background(), fake, agent, 2)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range choices {
		got = append(got, c.Ref+map[bool]string{true: " exhausted"}[c.RetriesExhausted])
	}
	if want := []string{"APP-SPNT exhausted", "APP-LOW1"}; !slices.Equal(got, want) {
		t.Errorf("choices = %q, want %q", got, want)
	}
}
