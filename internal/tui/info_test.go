package tui

import (
	"strings"
	"testing"
	"time"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/lock"
)

func infoModel(t *testing.T, agents []Agent, cfg *config.Config, pid int) Model {
	t.Helper()
	m := newTestModel(t, nil)
	m.deps.Project.Dir = "/home/me/my-app"
	m.deps.Agents = func(time.Time) []Agent { return agents }
	m.deps.LoadConfig = func() (*config.Config, error) { return cfg, nil }
	m.deps.SchedulerPID = func() int { return pid }
	m.agents = newAgentsState().reload(agents)
	m.info = m.refreshInfo()
	return press(t, m, "3")
}

func infoText(m Model) string {
	return xansi.Strip(strings.Join(m.infoLines(), "\n"))
}

var testConfig = &config.Config{
	BaseBranch: "main", Platform: "github", WorktreeDir: ".vteam-worktrees",
	Tasks:        config.Tasks{Project: "my-app", Label: "vteam", MaxRetries: 3},
	MergeEnsurer: config.MergeEnsurer{Cron: "*/15 * * * *", Label: "vteam:changes-requested"},
}

func TestInfoShowsProjectConfigAndScheduler(t *testing.T) {
	out := infoText(infoModel(t, nil, testConfig, 4242))
	for _, want := range []string{
		"▍ Project", "my-app (from aven tasks.project)", "/home/me/my-app", "branch        main",
		"▍ vteam.config.json", "platform      github", "worktreeDir   .vteam-worktrees", "maxRetries    3",
		"merge cron    */15 * * * *", "merge label   vteam:changes-requested",
		"▍ Scheduler", "running (pid 4242)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("info is missing %q:\n%s", want, out)
		}
	}
}

func TestInfoReportsConfigErrorAndStoppedScheduler(t *testing.T) {
	m := newTestModel(t, nil)
	out := infoText(press(t, m, "3"))
	if !strings.Contains(out, "error         no config") || !strings.Contains(out, "not running") {
		t.Errorf("want config error and stopped scheduler:\n%s", out)
	}
}

func TestInfoAgentDetails(t *testing.T) {
	agents := []Agent{
		{Name: "coder", Running: &lock.Info{PID: 1}, Config: &config.Agent{
			Model: "sonnet", Input: "task", Cron: "*/5 * * * *", Worktree: true, AutoPR: true,
			TaskFilter:   config.TaskFilter{"found-by": {"improver", ""}},
			AllowedTools: []string{"Read", "Bash(git *)"}, DisallowedTools: []string{"WebFetch"},
		}, Cron: "*/5 * * * *", NextRun: fixedNow.Add(5 * time.Minute)},
		{Name: "plain", Config: &config.Agent{}},
		{Name: "defaulted", Config: &config.Agent{Cron: "0 * * * *"}, DefaultCron: "0 * * * *"},
		{Name: "broken", LoadError: "invalid agent.json\nmore detail"},
		{Name: "retired"},
		{Name: "merge-ensurer", Builtin: true},
	}
	out := infoText(infoModel(t, agents, testConfig, 4242))
	for _, want := range []string{
		"● coder", "model     sonnet", "in task  out generic", "*/5 * * * *  next", "worktree readOnly autoPR",
		"filter    found-by: improver | (unset)", "tools     Read, Bash(git *)", "denied    WebFetch",
		"○ plain", "model     default", "cron      off\n", "cron      off  default 0 * * * *", "crons         1 enabled", "tools     Claude Code default set",
		"✗ broken", "invalid agent.json", "no agent directory (logs only)", "built-in sweep (vteam ensure-merge)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("info is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "more detail") {
		t.Error("only the first line of a load error should show")
	}
}

func TestInfoShowsPausedCronsWhenTheSchedulerIsDown(t *testing.T) {
	agents := []Agent{{Name: "coder", Config: &config.Agent{}, Cron: "*/5 * * * *"}}
	out := infoText(infoModel(t, agents, testConfig, 0))
	for _, want := range []string{"not running · 1 crons paused", "cron      */5 * * * *  paused"} {
		if !strings.Contains(out, want) {
			t.Errorf("info is missing %q:\n%s", want, out)
		}
	}
}

func TestPackItemsNeverSplitsAnItem(t *testing.T) {
	rows := packItems([]string{"Read", "Bash(npm view *)", "Bash(pnpm audit --fix *)"}, 24)
	want := []string{"Read, Bash(npm view *),", "Bash(pnpm audit --fix *)"}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Errorf("rows = %q", rows)
	}
}

func TestInfoScrolls(t *testing.T) {
	var agents []Agent
	for range 10 {
		agents = append(agents, Agent{Name: "a", Config: &config.Agent{}})
	}
	m := infoModel(t, agents, testConfig, 0)
	m = press(t, m, "G")
	if want := len(m.infoLines()) - m.bodyHeight(); m.info.offset != want || want <= 0 {
		t.Fatalf("G offset = %d, want %d", m.info.offset, want)
	}
	if m = press(t, m, "g", "g"); m.info.offset != 0 {
		t.Fatal("gg should return to the top")
	}
}
