package status

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/lock"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/runstate"
	"github.com/leodnlv/vteam/internal/tasks/taskstest"
)

func TestMain(m *testing.M) {
	os.Setenv("NO_COLOR", "1")
	os.Exit(m.Run())
}

func write(t *testing.T, path, content string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func agent(t *testing.T, cwd, name, agentJSON string) {
	write(t, filepath.Join(cwd, "vteam", "agents", name, "AGENT.md"), "prompt")
	write(t, filepath.Join(cwd, "vteam", "agents", name, "agent.json"), agentJSON)
}

var cfg = &config.Config{WorktreeDir: ".wt", Tasks: config.Tasks{MaxRetries: 3, Project: "app", Label: "vteam"}, MergeEnsurer: config.MergeEnsurer{Label: "x"}}

func deps(cwd string, fake *taskstest.Fake, now time.Time, orphans []string) Deps {
	return Deps{
		Cwd: cwd, Config: cfg, Tasks: fake, Now: func() time.Time { return now }, Host: "box",
		IsAlive: func(pid int) bool { return pid == os.Getpid() },
		Orphans: func(context.Context, string, string, []string) ([]string, error) { return orphans, nil },
	}
}

func task(ref, title string, status model.TaskStatus, sev model.Severity, meta map[string]string) model.Task {
	return model.Task{Ref: ref, Title: title, Status: status, Severity: sev, Metadata: meta, CreatedAt: "2026-01-01T00:00:00Z"}
}

func TestRenderEverything(t *testing.T) {
	cwd := t.TempDir()
	now := time.Now()
	agent(t, cwd, "coder", `{"input":"task","worktree":true,"taskFilter":{"found-by":["improver"]}}`)
	agent(t, cwd, "fixer", `{"input":"pr","worktree":true,"prTriggerLabel":"vteam:fix"}`)
	agent(t, cwd, "reviewer", `{"input":"pr","worktree":true}`)
	agent(t, cwd, "broken", `{"bogus":true}`)

	pid := strconv.Itoa(os.Getpid())
	lockDir := filepath.Join(runstate.LocksDir(cwd), "coder.lock")
	info, _ := json.Marshal(lock.Info{PID: os.Getpid(), Timestamp: now.Add(-5 * time.Minute).UnixMilli(), Agent: "coder"})
	write(t, filepath.Join(lockDir, "info.json"), string(info))
	_ = runstate.Write(cwd, model.RunState{RunID: "20260101-000000-coder", Agent: "coder", StartedAt: model.ISOTime(now.Add(-6 * time.Minute)),
		WorktreePath: filepath.Join(cwd, ".wt", "vteam", "fix")})

	exhausted := task("APP-4", "Tried too often", "todo", "medium", map[string]string{"found-by": "improver"})
	exhausted.RetryCount = 3
	fake := taskstest.New(
		task("APP-1", "Fix the thing", "active", "high", map[string]string{"claimed-pid": pid, "claimed-host": "box"}),
		task("APP-2", "Next up", "todo", "critical", map[string]string{"found-by": "improver", "pr-url": "u2"}),
		task("APP-3", "Nobody wants me", "todo", "low", map[string]string{"found-by": "human"}),
		exhausted,
		task("APP-5", "Awaiting", "done", "low", map[string]string{"pr-url": "https://github.com/o/r/pull/5"}),
		task("APP-6", "Merged", "done", "low", map[string]string{"pr-url": "u6", "merged": "true"}),
		task("APP-7", "Inbox one", "inbox", "low", nil),
		task("APP-8", "Hand-set active", "active", "low", nil),
		task("APP-9", "Orphaned", "active", "low", map[string]string{"claimed-pid": "999999", "claimed-host": "box"}),
		task("APP-10", "Elsewhere", "active", "low", map[string]string{"claimed-pid": "999999", "claimed-host": "other"}),
	)

	lines, err := Render(context.Background(), deps(cwd, fake, now, []string{filepath.Join(cwd, ".wt", "vteam", "idle")}))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"vteam ▸ status   aven: app · label: vteam",
		log.FormatSection("Running"),
		"  ● coder  pid " + pid + " · 5m  APP-1 [high] Fix the thing",
		"    worktree .wt/vteam/fix",
		log.FormatSection("Scheduler"),
		"  · no agents have a cron schedule",
		log.FormatSection("Up next"),
		"  • coder     APP-2 [critical] Next up (PR follow-up)",
		`  • fixer     open PRs labelled "vteam:fix"`,
		"  • reviewer  any open PR with review feedback",
		log.FormatSection("Awaiting merge (1)"),
		"  • APP-5 Awaiting  https://github.com/o/r/pull/5",
		log.FormatSection("Needs attention"),
		`  ✗ agent "broken" failed to load: invalid config in broken/agent.json:` + "\n" + `      - (root): Unrecognized key: "bogus"`,
		"  ! 1 task(s) in inbox awaiting triage",
		"  ✗ APP-3 matches no agent's taskFilter and will never be picked",
		"  ✗ APP-4 hit max retries (3/3) and will not be picked",
		"  ! APP-8 is active without a vteam claim (reset it by hand if it is stuck)",
		"  ✗ APP-9 is active but its process (pid 999999) is dead (run 'vteam clean')",
		"  ! 1 worktree(s) with no running agent (run 'vteam clean')\n    .wt/vteam/idle",
	}
	if diff := cmp.Diff(want, lines); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestRenderQuietProject(t *testing.T) {
	cwd := t.TempDir()
	agent(t, cwd, "improver", `{"output":"task"}`)
	lines, err := Render(context.Background(), deps(cwd, taskstest.New(), time.Now(), nil))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "· nothing running") {
		t.Errorf("output:\n%s", joined)
	}
	for _, absent := range []string{"Up next", "Awaiting merge", "Needs attention"} {
		if strings.Contains(joined, absent) {
			t.Errorf("unexpected %q in:\n%s", absent, joined)
		}
	}
}

func TestRenderNothingReady(t *testing.T) {
	cwd := t.TempDir()
	agent(t, cwd, "coder", `{"input":"task"}`)
	lines, _ := Render(context.Background(), deps(cwd, taskstest.New(), time.Now(), nil))
	if !strings.Contains(strings.Join(lines, "\n"), "  • coder  nothing ready") {
		t.Errorf("output:\n%s", strings.Join(lines, "\n"))
	}
}

func TestRenderIgnoresOrphanLookupErrors(t *testing.T) {
	cwd := t.TempDir()
	d := deps(cwd, taskstest.New(), time.Now(), nil)
	d.Orphans = func(context.Context, string, string, []string) ([]string, error) { return nil, errors.New("not a git repo") }
	if _, err := Render(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

type failingTasks struct{ *taskstest.Fake }

func (failingTasks) ListReady(context.Context) ([]model.Task, error) { return nil, errors.New("aven down") }

func TestRenderFailsWhenTasksCannotBeRead(t *testing.T) {
	d := deps(t.TempDir(), taskstest.New(), time.Now(), nil)
	d.Tasks = failingTasks{taskstest.New()}
	if _, err := Render(context.Background(), d); err == nil || !strings.Contains(err.Error(), "aven down") {
		t.Errorf("err = %v", err)
	}
}
