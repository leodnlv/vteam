package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/leodnlv/vteam/internal/claude"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/tasks"
	"github.com/leodnlv/vteam/internal/tasks/taskstest"
)

func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	os.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	os.Setenv("NO_COLOR", "1")
	os.Exit(m.Run())
}

type obj = map[string]any

type project struct {
	onFinish, memory bool
	existingMemory   string
	agent            obj
}

type harness struct {
	t        *testing.T
	cwd      string
	tasks    *taskstest.Fake
	claude   *fakeClaude
	git      *fakeWorktrees
	platform *fakePlatform
	workmux  *fakeWorkmux
	notifier *fakeNotifier
	out      syncBuffer
	logger   *log.Logger
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeAgent(t *testing.T, cwd, name string, cfg obj, prompt string) {
	data, _ := json.Marshal(cfg)
	dir := filepath.Join(cwd, "vteam", "agents", name)
	writeFile(t, filepath.Join(dir, "agent.json"), string(data))
	writeFile(t, filepath.Join(dir, "AGENT.md"), prompt)
}

func setup(t *testing.T, p project) *harness {
	t.Helper()
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "vteam", "vteam.config.json"),
		`{"baseBranch":"main","platform":"github","worktreeDir":".worktrees","tasks":{"maxRetries":3,"project":"test"}}`)
	cfg := obj{"model": "sonnet"}
	for k, v := range p.agent {
		cfg[k] = v
	}
	if p.onFinish {
		cfg["onFinish"] = obj{"model": "haiku"}
		writeFile(t, filepath.Join(cwd, "vteam", "agents", "test-agent", "ON_FINISH.md"), "Post notification.")
	}
	if p.memory {
		cfg["memory"] = obj{"model": "haiku"}
		writeFile(t, filepath.Join(cwd, "vteam", "agents", "test-agent", "MEMORY.md"), "Keep a running list of scanned areas.")
	}
	writeAgent(t, cwd, "test-agent", cfg, "You are a test agent.")
	if p.existingMemory != "" {
		writeFile(t, MemoryPath(cwd, "test-agent"), p.existingMemory)
	}
	h := &harness{t: t, cwd: cwd, tasks: taskstest.New(), claude: &fakeClaude{}, git: newFakeWorktrees(t, cwd)}
	h.platform = &fakePlatform{order: &h.git.order}
	h.workmux = &fakeWorkmux{git: h.git}
	h.notifier = &fakeNotifier{}
	h.logger = log.New(&h.out, &h.out)
	return h
}

func (h *harness) deps() Deps {
	cfg, err := config.Load(h.cwd)
	if err != nil {
		h.t.Fatal(err)
	}
	return Deps{Cwd: h.cwd, Config: cfg, Log: h.logger, Tasks: h.tasks, Claude: h.claude, Git: h.git, Platform: h.platform,
		Workmux: h.workmux, Notifier: h.notifier, Bin: "vteam", Now: time.Now, PID: 4242, Host: "test-host", LockTimeout: 10 * time.Second}
}

func (h *harness) run(agentName, focus string) error {
	h.t.Helper()
	return h.runWith(agentName, Options{Focus: focus})
}

func (h *harness) runWith(agentName string, opts Options) error {
	h.t.Helper()
	agent, err := config.ResolveAgent(agentName, h.cwd)
	if err != nil {
		h.t.Fatal(err)
	}
	return Run(context.Background(), h.deps(), agent, opts)
}

func (h *harness) mustRun() {
	h.t.Helper()
	if err := h.run("test-agent", ""); err != nil {
		h.t.Fatalf("run failed: %v\n%s", err, h.out.String())
	}
}

func jsonText(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func (h *harness) statuses() []model.TaskStatus {
	var out []model.TaskStatus
	for _, u := range h.tasks.Updates() {
		out = append(out, u.Update.Status)
	}
	return out
}

func (h *harness) lastUpdate() tasks.Update {
	updates := h.tasks.Updates()
	if len(updates) == 0 {
		h.t.Fatal("no updates")
	}
	return updates[len(updates)-1].Update
}

func (h *harness) prompt(i int) claude.Options {
	h.t.Helper()
	calls := h.claude.Calls()
	if len(calls) <= i {
		h.t.Fatalf("only %d claude calls", len(calls))
	}
	return calls[i]
}

func requireContains(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(text, p) {
			t.Errorf("missing %q in:\n%s", p, text)
		}
	}
}

func requireNotContains(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(text, p) {
			t.Errorf("unexpected %q in:\n%s", p, text)
		}
	}
}

func TestAgentOutputToOnFinish(t *testing.T) {
	t.Run("passes generic content to ON_FINISH hook", func(t *testing.T) {
		h := setup(t, project{onFinish: true})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Review done.", "content": obj{"type": "generic", "body": "All looks good."}})).thenText("hook ok")
		h.mustRun()
		if n := len(h.claude.Calls()); n != 2 {
			t.Fatalf("%d calls", n)
		}
		hook := h.prompt(1)
		requireContains(t, hook.UserPrompt, "completed", "All looks good.")
		requireContains(t, hook.SystemPrompt, "Post notification.")
		if hook.Dir != h.cwd || hook.Model != "haiku" {
			t.Errorf("hook ran in %q with model %q", hook.Dir, hook.Model)
		}
	})

	t.Run("passes task content as JSON to ON_FINISH hook", func(t *testing.T) {
		h := setup(t, project{onFinish: true, agent: obj{"output": "task"}})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Found issue.", "content": obj{"type": "task", "body": obj{
			"title": "Null pointer in auth", "severity": "high", "description": "NPE when user.email is null", "files": []string{"src/auth.ts:45"}}}})).thenText("hook ok")
		h.mustRun()
		requireContains(t, h.prompt(1).UserPrompt, `"title": "Null pointer in auth"`, "high")
	})

	t.Run("runs ON_FINISH with error details when agent fails", func(t *testing.T) {
		h := setup(t, project{onFinish: true})
		h.claude.then(func(claude.Options) claude.Result { return result("", 1) }).thenText("hook ok")
		if err := h.run("test-agent", ""); !errors.Is(err, ErrFailed) {
			t.Fatalf("err = %v", err)
		}
		requireContains(t, h.prompt(1).UserPrompt, "failed", "claude exited with code 1")
		requireContains(t, h.out.String(), "✗ test-agent failed after")
	})

	t.Run("includes the agent name and status in ON_FINISH", func(t *testing.T) {
		h := setup(t, project{onFinish: true})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done.", "content": obj{"type": "generic", "body": "Result."}})).thenText("hook ok")
		h.mustRun()
		requireContains(t, h.prompt(1).UserPrompt, "- Agent: test-agent", "- Status: completed")
	})

	t.Run("does not fail the run when the hook fails", func(t *testing.T) {
		h := setup(t, project{onFinish: true})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done."}))
		h.mustRun()
		requireContains(t, h.out.String(), "on-finish hook failed")
	})
}

func TestAgentOutputToMemory(t *testing.T) {
	t.Run("passes memoryUpdate to curation agent", func(t *testing.T) {
		h := setup(t, project{memory: true})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Scanned auth module.", "memoryUpdate": "Scanned src/auth.ts — found singleton pattern."})).thenText("Updated memory content.")
		h.mustRun()
		curation := h.prompt(1)
		requireContains(t, curation.UserPrompt, "Scanned src/auth.ts — found singleton pattern.", "## New Update")
		requireContains(t, curation.SystemPrompt, "Keep a running list of scanned areas.")
	})

	t.Run("writes curation result to store.md", func(t *testing.T) {
		h := setup(t, project{memory: true})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done.", "memoryUpdate": "New info."})).thenText("  - Scanned auth module\n- Found singleton pattern\n\n")
		h.mustRun()
		data, err := os.ReadFile(MemoryPath(h.cwd, "test-agent"))
		if err != nil || string(data) != "- Scanned auth module\n- Found singleton pattern\n" {
			t.Errorf("store = %q err %v", data, err)
		}
	})

	t.Run("includes existing memory in curation prompt", func(t *testing.T) {
		h := setup(t, project{memory: true, existingMemory: "Previously: scanned foo/"})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done.", "memoryUpdate": "Now: scanned bar/"})).thenText("Merged memory.")
		h.mustRun()
		requireContains(t, h.prompt(1).UserPrompt, "Previously: scanned foo/", "Now: scanned bar/")
	})

	t.Run("keeps the old memory when curation produces nothing", func(t *testing.T) {
		h := setup(t, project{memory: true, existingMemory: "kept"})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done.", "memoryUpdate": "x"})).then(func(claude.Options) claude.Result { return result("", 1) })
		h.mustRun()
		if data, _ := os.ReadFile(MemoryPath(h.cwd, "test-agent")); string(data) != "kept" {
			t.Errorf("store = %q", data)
		}
		requireContains(t, h.out.String(), "memory curation produced no output or failed")
	})

	t.Run("reports a memory store that cannot be written", func(t *testing.T) {
		h := setup(t, project{memory: true})
		writeFile(t, filepath.Join(h.cwd, "vteam", ".memory"), "a file where the directory should be")
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done.", "memoryUpdate": "x"})).thenText("curated")
		h.mustRun()
		requireContains(t, h.out.String(), "memory curation failed")
		requireNotContains(t, h.out.String(), "memory updated")
	})

	t.Run("skips curation when agent returns no memoryUpdate", func(t *testing.T) {
		h := setup(t, project{memory: true})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done."}))
		h.mustRun()
		if n := len(h.claude.Calls()); n != 1 {
			t.Errorf("%d calls", n)
		}
	})

	t.Run("skips curation when agent has no MEMORY.md even if memoryUpdate present", func(t *testing.T) {
		h := setup(t, project{})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done.", "memoryUpdate": "Something to remember."}))
		h.mustRun()
		if n := len(h.claude.Calls()); n != 1 {
			t.Errorf("%d calls", n)
		}
	})

	t.Run("injects existing memory into agent's user prompt", func(t *testing.T) {
		h := setup(t, project{memory: true, existingMemory: "Previous run: scanned auth/"})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done."}))
		h.mustRun()
		requireContains(t, h.prompt(0).UserPrompt, "Previous run: scanned auth/", "Agent Memory")
	})
}

func TestAgentOutputToTaskCreation(t *testing.T) {
	t.Run("creates a task when agent returns task content", func(t *testing.T) {
		h := setup(t, project{agent: obj{"output": "task"}})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Found issue.", "content": obj{"type": "task", "body": obj{
			"title": "Missing error handling", "severity": "medium", "description": "No try-catch around DB call", "files": []string{"src/db.ts:20"}}}}))
		h.mustRun()
		want := []taskstest.Created{{Ref: "VTM-1", Body: model.TaskContentBody{Title: "Missing error handling", Severity: "medium",
			Description: "No try-catch around DB call", Files: []string{"src/db.ts:20"}}, FoundBy: "test-agent", Labels: []string{"vteam-test-agent"}}}
		if diff := cmp.Diff(want, h.tasks.Created()); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("does not create a task for generic content", func(t *testing.T) {
		h := setup(t, project{})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done.", "content": obj{"type": "generic", "body": "All good."}}))
		h.mustRun()
		if len(h.tasks.Created()) != 0 {
			t.Error("task created")
		}
	})

	t.Run("uses the schema-validated answer when the final text is prose", func(t *testing.T) {
		h := setup(t, project{agent: obj{"output": "task"}})
		h.claude.then(func(claude.Options) claude.Result {
			r := result("Well shoot, I sent it in through the findings tool.", 0)
			r.StructuredOutput = jsonText(obj{"status": "completed", "summary": "Found issue.", "content": obj{"type": "task", "body": obj{
				"title": "Stale lock race", "severity": "medium", "description": "Two waiters can both break it.", "files": []string{"src/lock.ts:86"}}}})
			return r
		})
		h.mustRun()
		if created := h.tasks.Created(); len(created) != 1 || created[0].Body.Title != "Stale lock race" {
			t.Errorf("created = %+v", created)
		}
		if schema := h.claude.Calls()[0].JSONSchema; !strings.Contains(schema, `"task"`) {
			t.Errorf("agent run should pass the task output schema, got %q", schema)
		}
	})

	t.Run("fails the run on invalid output", func(t *testing.T) {
		h := setup(t, project{})
		h.claude.thenText("I could not finish.")
		if err := h.run("test-agent", ""); !errors.Is(err, ErrFailed) {
			t.Errorf("err = %v", err)
		}
	})
}

func seedTask(h *harness, t model.Task) {
	base := model.Task{Ref: "VTM-ABCD", Title: "Fix the thing", Status: "todo", Severity: "high", Description: "It is broken.", CreatedAt: "2026-10-01T00:00:00Z"}
	if t.Ref != "" {
		base.Ref = t.Ref
	}
	if t.Severity != "" {
		base.Severity = t.Severity
	}
	base.RetryCount = t.RetryCount
	base.Metadata = t.Metadata
	base.Notes = t.Notes
	h.tasks.Add(base)
}

func TestTaskInputLifecycle(t *testing.T) {
	looked := jsonText(obj{"status": "completed", "summary": "Looked."})

	t.Run("marks the picked task active, then returns it to todo when not settled", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task"}})
		seedTask(h, model.Task{})
		h.claude.thenText(looked)
		h.mustRun()
		if diff := cmp.Diff([]model.TaskStatus{"active", "todo"}, h.statuses()); diff != "" {
			t.Error(diff)
		}
		requireContains(t, h.prompt(0).UserPrompt, "It is broken.")
	})

	t.Run("claims the task with run metadata and clears the claim on release", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task"}})
		seedTask(h, model.Task{})
		h.claude.thenText(looked)
		h.mustRun()
		updates := h.tasks.Updates()
		claim, release := updates[0].Update.Metadata, updates[1].Update
		if !regexp.MustCompile(`^\d{8}-\d{6}-test-agent$`).MatchString(claim["claimed-by"]) || claim["claimed-pid"] != "4242" || claim["claimed-host"] != "test-host" {
			t.Errorf("claim = %v", claim)
		}
		if diff := cmp.Diff([]string{"claimed-by", "claimed-pid", "claimed-host"}, release.RemoveMetadata); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("releases the claim lock before the agent runs", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task"}})
		seedTask(h, model.Task{})
		claimLock := filepath.Join(LocksDir(h.cwd), "task-claim.lock")
		var held bool
		h.claude.then(func(claude.Options) claude.Result {
			_, err := os.Stat(claimLock)
			held = err == nil
			return result(looked, 0)
		})
		h.mustRun()
		if held {
			t.Error("claim lock held while the agent ran")
		}
	})

	t.Run("never hands the same task to two agents running at once", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task"}})
		writeAgent(t, h.cwd, "other-agent", obj{"model": "sonnet", "input": "task"}, "Other.")
		seedTask(h, model.Task{})
		h.tasks.OnListReady = func() { time.Sleep(20 * time.Millisecond) }
		gate := make(chan struct{})
		h.claude.fallback = func(claude.Options) claude.Result {
			<-gate
			return result(looked, 0)
		}
		var wg sync.WaitGroup
		for _, name := range []string{"test-agent", "other-agent"} {
			wg.Go(func() { _ = h.run(name, "") })
		}
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(h.out.String(), "no eligible tasks") && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		close(gate)
		wg.Wait()
		active := 0
		for _, s := range h.statuses() {
			if s == "active" {
				active++
			}
		}
		if active != 1 || len(h.claude.Calls()) != 1 {
			t.Errorf("active claims %d, claude calls %d", active, len(h.claude.Calls()))
		}
	})

	t.Run("notes the agent's summary and blocker reason on the task", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task"}})
		seedTask(h, model.Task{})
		h.claude.thenText(jsonText(obj{"status": "blocked", "summary": "Looked.", "blockerReason": "No DB."}))
		h.mustRun()
		want := []taskstest.Note{{Ref: "VTM-ABCD", Text: "test-agent: blocked\n\nLooked.\n\nBlocker: No DB."}}
		if diff := cmp.Diff(want, h.tasks.Notes()); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("still releases the task when the note cannot be written", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task"}})
		seedTask(h, model.Task{})
		h.tasks.FailNotes = true
		h.claude.thenText(looked)
		h.mustRun()
		if diff := cmp.Diff([]model.TaskStatus{"active", "todo"}, h.statuses()); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("returns the task to todo when the agent fails", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task"}})
		seedTask(h, model.Task{})
		h.claude.then(func(claude.Options) claude.Result { return result("", 1) })
		if err := h.run("test-agent", ""); !errors.Is(err, ErrFailed) {
			t.Fatalf("err = %v", err)
		}
		if diff := cmp.Diff([]model.TaskStatus{"active", "todo"}, h.statuses()); diff != "" || len(h.tasks.Notes()) != 0 {
			t.Errorf("statuses %v notes %v", h.statuses(), h.tasks.Notes())
		}
	})

	t.Run("reports a failed release without failing the run", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task"}})
		seedTask(h, model.Task{})
		h.tasks.FailUpdate = func(_ string, u tasks.Update) error {
			if u.Status == model.StatusTodo {
				return errors.New("db locked")
			}
			return nil
		}
		h.claude.thenText(looked)
		h.mustRun()
		requireContains(t, h.out.String(), "failed to return task VTM-ABCD to todo: db locked")
	})

	t.Run("picks the highest severity task and skips tasks over the retry limit", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task"}})
		seedTask(h, model.Task{Ref: "VTM-LOW1", Severity: "low"})
		seedTask(h, model.Task{Ref: "VTM-CRIT", Severity: "critical", RetryCount: 3})
		seedTask(h, model.Task{Ref: "VTM-HIGH", Severity: "high"})
		h.claude.thenText(looked)
		h.mustRun()
		if first := h.tasks.Updates()[0]; first.Ref != "VTM-HIGH" || first.Update.Status != "active" {
			t.Errorf("first update = %+v", first)
		}
	})

	pickedRef := func(h *harness) string {
		h.claude.thenText(looked)
		h.mustRun()
		for _, u := range h.tasks.Updates() {
			if u.Update.Status == "active" {
				return u.Ref
			}
		}
		return ""
	}

	t.Run("picks only tasks matching its taskFilter, ORing list values", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task", "taskFilter": obj{"found-by": []string{"improver", "security-analyst"}}}})
		seedTask(h, model.Task{Ref: "VTM-OTHR", Severity: "critical", Metadata: map[string]string{"found-by": "human"}})
		seedTask(h, model.Task{Ref: "VTM-SECU", Severity: "low", Metadata: map[string]string{"found-by": "security-analyst"}})
		if ref := pickedRef(h); ref != "VTM-SECU" {
			t.Errorf("picked %q", ref)
		}
	})

	t.Run("matches a missing metadata key with null", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task", "taskFilter": obj{"actor": nil}}})
		seedTask(h, model.Task{Ref: "VTM-REVW", Severity: "critical", Metadata: map[string]string{"actor": "mr-reviewer"}})
		seedTask(h, model.Task{Ref: "VTM-FREE", Severity: "low"})
		if ref := pickedRef(h); ref != "VTM-FREE" {
			t.Errorf("picked %q", ref)
		}
	})

	t.Run("picks every task when it has no taskFilter", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task"}})
		seedTask(h, model.Task{Ref: "VTM-REVW", Severity: "critical", Metadata: map[string]string{"actor": "mr-reviewer"}})
		if ref := pickedRef(h); ref != "VTM-REVW" {
			t.Errorf("picked %q", ref)
		}
	})

	t.Run("stays idle when no ready task matches its taskFilter", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task", "taskFilter": obj{"actor": "mr-reviewer"}}})
		seedTask(h, model.Task{Ref: "VTM-OTHR", Metadata: map[string]string{"found-by": "improver"}})
		h.mustRun()
		if len(h.claude.Calls()) != 0 || len(h.tasks.Updates()) != 0 {
			t.Error("agent ran")
		}
		requireContains(t, h.out.String(), "no eligible tasks, nothing to do")
	})

	t.Run("claims the task chosen with --task over priority and the retry limit", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task"}})
		seedTask(h, model.Task{Ref: "VTM-CRIT", Severity: "critical"})
		seedTask(h, model.Task{Ref: "VTM-SPNT", Severity: "low", RetryCount: 3})
		h.claude.thenText(looked)
		if err := h.runWith("test-agent", Options{Task: "vtm-spnt"}); err != nil {
			t.Fatalf("run failed: %v\n%s", err, h.out.String())
		}
		if first := h.tasks.Updates()[0]; first.Ref != "VTM-SPNT" || first.Update.Status != "active" {
			t.Errorf("first update = %+v", first)
		}
		requireContains(t, h.out.String(), "picked task VTM-SPNT", "chosen with --task")
	})

	for name, tc := range map[string]struct {
		agent obj
		task  model.Task
		want  string
	}{
		"fails without claiming when the chosen task is not ready": {
			agent: obj{"input": "task"},
			task:  model.Task{Ref: "VTM-OTHR"},
			want:  "task VTM-GONE is not ready",
		},
		"fails without claiming when the chosen task is not routed to the agent": {
			agent: obj{"input": "task", "taskFilter": obj{"actor": "mr-reviewer"}},
			task:  model.Task{Ref: "VTM-GONE"},
			want:  "task VTM-GONE is not routed to test-agent",
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := setup(t, project{agent: tc.agent})
			seedTask(h, tc.task)
			if err := h.runWith("test-agent", Options{Task: "VTM-GONE"}); !errors.Is(err, ErrFailed) {
				t.Fatalf("err = %v", err)
			}
			if len(h.claude.Calls()) != 0 || len(h.tasks.Updates()) != 0 {
				t.Error("agent ran or a task was updated")
			}
			requireContains(t, h.out.String(), tc.want)
		})
	}

	t.Run("does nothing when no task is ready", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task"}})
		h.mustRun()
		if len(h.claude.Calls()) != 0 || len(h.tasks.Updates()) != 0 {
			t.Error("agent ran")
		}
	})
}

func TestCombinedOnFinishAndMemory(t *testing.T) {
	t.Run("runs both ON_FINISH and memory curation when both configured", func(t *testing.T) {
		h := setup(t, project{onFinish: true, memory: true})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done.", "content": obj{"type": "generic", "body": "Review report."}, "memoryUpdate": "Scanned module X."})).
			thenText("hook ok").thenText("Updated memory.")
		h.mustRun()
		if len(h.claude.Calls()) != 3 {
			t.Fatalf("%d calls", len(h.claude.Calls()))
		}
		requireContains(t, h.prompt(1).UserPrompt, "Review report.")
		requireContains(t, h.prompt(2).UserPrompt, "Scanned module X.")
	})

	t.Run("runs ON_FINISH even when memory curation is skipped", func(t *testing.T) {
		h := setup(t, project{onFinish: true, memory: true})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done.", "content": obj{"type": "generic", "body": "Report."}})).thenText("hook ok")
		h.mustRun()
		if len(h.claude.Calls()) != 2 {
			t.Errorf("%d calls", len(h.claude.Calls()))
		}
	})

	t.Run("makes no extra calls when neither ON_FINISH nor MEMORY configured", func(t *testing.T) {
		h := setup(t, project{})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done."}))
		h.mustRun()
		if len(h.claude.Calls()) != 1 {
			t.Errorf("%d calls", len(h.claude.Calls()))
		}
	})
}

func TestFocusFlow(t *testing.T) {
	t.Run("passes focus to ON_FINISH hook", func(t *testing.T) {
		h := setup(t, project{onFinish: true})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Investigated error.", "content": obj{"type": "generic", "body": "Root cause."}})).thenText("hook ok")
		if err := h.run("test-agent", "https://sentry.io/issues/123"); err != nil {
			t.Fatal(err)
		}
		requireContains(t, h.prompt(0).UserPrompt, "## Priority Focus")
		requireContains(t, h.prompt(1).UserPrompt, "Focus Context", "https://sentry.io/issues/123")
	})

	t.Run("passes focus to memory curation", func(t *testing.T) {
		h := setup(t, project{memory: true})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done.", "memoryUpdate": "Investigated auth error."})).thenText("Updated memory.")
		if err := h.run("test-agent", "https://sentry.io/issues/456"); err != nil {
			t.Fatal(err)
		}
		requireContains(t, h.prompt(1).UserPrompt, "Run Context", "https://sentry.io/issues/456")
	})

	t.Run("omits focus sections when no focus provided", func(t *testing.T) {
		h := setup(t, project{onFinish: true, memory: true})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done.", "content": obj{"type": "generic", "body": "Report."}, "memoryUpdate": "Scanned X."})).
			thenText("hook ok").thenText("Updated memory.")
		h.mustRun()
		requireNotContains(t, h.prompt(1).UserPrompt, "Focus Context")
		requireNotContains(t, h.prompt(2).UserPrompt, "Run Context")
	})
}

const (
	prURL    = "https://github.com/org/repo/pull/7"
	prBranch = "vteam/fix-the-thing-20261001-000000-coder"
)

func seedDoneTaskReopened(h *harness) {
	h.tasks.Add(model.Task{
		Ref: "VTM-FLUP", Title: "Fix the thing", Status: "todo", Severity: "high",
		Metadata:    map[string]string{"branch": prBranch, "pr-url": prURL, "completed": "2026-10-01T00:00:00Z"},
		Description: "It is broken.",
		Notes:       []model.TaskNote{{Body: "Apply the null-check thread, skip the rename.", CreatedAt: "2026-10-02T00:00:00Z"}},
		CreatedAt:   "2026-10-01T00:00:00Z",
	})
}

func mockPR(h *harness, state model.PRState, mergeable model.PRMergeability) {
	h.platform.pr = &model.PullRequestInfo{
		ReviewablePR: model.ReviewablePR{Number: 7, Title: "vteam: coder: Fix the thing", Branch: prBranch, URL: prURL},
		State:        state, BaseBranch: "main", Labels: []string{}, Mergeable: mergeable, Review: "none",
	}
}

func agentEditing(h *harness, extra obj, edits bool) {
	h.claude.then(func(opts claude.Options) claude.Result {
		if edits {
			writeFile(h.t, filepath.Join(opts.Dir, "thing.ts"), "fixed")
		}
		out := obj{"status": "completed", "summary": "Added the null check.", "filesChanged": []string{"thing.ts"},
			"commitMessage": obj{"subject": "vteam: guard null email", "body": "Review follow-up."}}
		for k, v := range extra {
			out[k] = v
		}
		return result(jsonText(out), 0)
	})
}

var threads = []model.ReviewThread{{
	ID: "PRRT_null", Path: "src/thing.ts", Line: 12,
	Comments: []model.ReviewComment{{Author: "alice", Body: "Missing null check here.", CreatedAt: "2026-10-01T12:00:00Z"}},
}}

func TestFollowUpOnExistingPR(t *testing.T) {
	worktreeTask := obj{"input": "task", "worktree": true, "autoPR": true}

	t.Run("checks out the PR branch, prompts with open threads, and pushes without opening a new PR", func(t *testing.T) {
		h := setup(t, project{agent: worktreeTask})
		seedDoneTaskReopened(h)
		mockPR(h, "open", "mergeable")
		h.platform.threads = threads
		agentEditing(h, nil, true)
		h.mustRun()

		if diff := cmp.Diff([]string{prURL}, h.platform.prLookups); diff != "" {
			t.Error(diff)
		}
		if diff := cmp.Diff([]string{h.cwd + "|" + prBranch + "|.worktrees"}, h.git.checked); diff != "" || len(h.git.created) != 0 {
			t.Errorf("checked %v created %v", h.git.checked, h.git.created)
		}
		requireContains(t, h.prompt(0).UserPrompt, "## Existing Pull Request", "**alice:** Missing null check here.", "Apply the null-check thread, skip the rename.")
		if len(h.git.pushes) != 1 || h.git.pushes[0].Branch != prBranch || h.git.pushes[0].RemoteSHA != "pr-head-sha" {
			t.Errorf("pushes = %+v", h.git.pushes)
		}
		if len(h.platform.created) != 0 {
			t.Error("opened a new PR")
		}
		done := h.lastUpdate()
		if done.Status != "done" || done.Metadata["branch"] != prBranch || done.Metadata["pr-url"] != prURL {
			t.Errorf("done = %+v", done)
		}
		requireContains(t, h.tasks.Notes()[0].Text, "PR: "+prURL)
		if msg := strings.TrimSpace(runGit(t, h.git.pushes[0].Path, "log", "-1", "--format=%B")); msg != "vteam: guard null email\n\nReview follow-up." {
			t.Errorf("commit message = %q", msg)
		}
		if len(h.git.removed) != 1 {
			t.Errorf("removed = %v", h.git.removed)
		}
	})

	for _, state := range []model.PRState{"merged", "closed"} {
		t.Run("moves the task to backlog without running when the PR is "+string(state), func(t *testing.T) {
			h := setup(t, project{agent: worktreeTask})
			seedDoneTaskReopened(h)
			mockPR(h, state, "mergeable")
			h.mustRun()
			if len(h.claude.Calls()) != 0 || len(h.git.checked) != 0 {
				t.Error("agent ran")
			}
			if diff := cmp.Diff([]model.TaskStatus{"active", "backlog"}, h.statuses()); diff != "" {
				t.Error(diff)
			}
			if diff := cmp.Diff(tasks.ClaimKeys, h.tasks.Updates()[1].Update.RemoveMetadata); diff != "" {
				t.Error(diff)
			}
			requireContains(t, h.tasks.Notes()[0].Text, "PR "+prURL+" is "+string(state))
		})
	}

	t.Run("starts a new branch and PR when the task has no pr-url", func(t *testing.T) {
		h := setup(t, project{agent: worktreeTask})
		h.tasks.Add(model.Task{Ref: "VTM-NEW1", Title: "Fix the thing", Status: "todo", Severity: "high", Description: "It is broken.", CreatedAt: "2026-10-01T00:00:00Z"})
		agentEditing(h, nil, true)
		h.mustRun()
		if len(h.git.pushes) != 1 || h.git.pushes[0].RemoteSHA != "" {
			t.Errorf("pushes = %+v", h.git.pushes)
		}
		if len(h.platform.prLookups) != 0 || len(h.git.checked) != 0 || len(h.platform.created) != 1 {
			t.Errorf("lookups %v checked %v created %v", h.platform.prLookups, h.git.checked, h.platform.created)
		}
		if !strings.HasPrefix(h.git.created[0], "Fix the thing-") {
			t.Errorf("slug = %q", h.git.created[0])
		}
		requireNotContains(t, h.prompt(0).UserPrompt, "Existing Pull Request")
		req := h.platform.created[0]
		if req.Title != "vteam: test-agent: Fix the thing" || req.Body != "Review follow-up." || req.BaseBranch != "main" {
			t.Errorf("PR request = %+v", req)
		}
		done := h.lastUpdate()
		if diff := cmp.Diff([]string{"vteam-test-agent"}, done.AddLabels); diff != "" || done.Metadata["pr-url"] != "https://github.com/org/repo/pull/99" {
			t.Errorf("done = %+v", done)
		}
	})

	t.Run("counts a retry when a worktree agent changes nothing", func(t *testing.T) {
		h := setup(t, project{agent: worktreeTask})
		seedTask(h, model.Task{RetryCount: 1})
		agentEditing(h, nil, false)
		h.mustRun()
		last := h.lastUpdate()
		if last.Status != "todo" || last.Metadata["retry-count"] != "2" {
			t.Errorf("last = %+v", last)
		}
		requireContains(t, h.out.String(), "test-agent produced no changes (completed)", "retries   2/3")
		if len(h.git.pushes) != 0 {
			t.Error("pushed")
		}
	})
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitOutput(t, dir, args...)
}

func TestMergeConflicts(t *testing.T) {
	setupConflicting := func(t *testing.T, conflicted []string) *harness {
		h := setup(t, project{agent: obj{"input": "task", "worktree": true}})
		seedDoneTaskReopened(h)
		mockPR(h, "open", "conflicting")
		h.git.conflict = conflicted
		return h
	}

	t.Run("does not merge the base branch when the PR is mergeable", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task", "worktree": true}})
		seedDoneTaskReopened(h)
		mockPR(h, "open", "mergeable")
		agentEditing(h, nil, true)
		h.mustRun()
		if len(h.git.merges) != 0 {
			t.Error("merged")
		}
		requireNotContains(t, h.prompt(0).UserPrompt, "## Merge Conflicts")
	})

	t.Run("merges the base branch, lists conflicted files in the prompt, and pushes the resolution", func(t *testing.T) {
		h := setupConflicting(t, []string{"thing.ts"})
		agentEditing(h, nil, true)
		h.mustRun()
		if len(h.git.merges) != 1 || !strings.Contains(h.git.merges[0], prBranch) || !strings.HasSuffix(h.git.merges[0], "|main") {
			t.Errorf("merges = %v", h.git.merges)
		}
		requireContains(t, h.prompt(0).UserPrompt, "## Merge Conflicts", "- `thing.ts`")
		if len(h.git.pushes) != 1 || h.git.pushes[0].RemoteSHA != "pr-head-sha" || h.lastUpdate().Status != "done" {
			t.Errorf("pushes %+v last %+v", h.git.pushes, h.lastUpdate())
		}
	})

	t.Run("commits nothing and counts a retry when conflict markers are left", func(t *testing.T) {
		h := setupConflicting(t, []string{"thing.ts"})
		h.claude.then(func(opts claude.Options) claude.Result {
			writeFile(t, filepath.Join(opts.Dir, "thing.ts"), "<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> origin/main\n")
			return result(jsonText(obj{"status": "completed", "summary": "Resolved conflicts.", "commitMessage": obj{"subject": "vteam: merge main", "body": ""}}), 0)
		})
		h.mustRun()
		if len(h.git.pushes) != 0 {
			t.Error("pushed")
		}
		last := h.lastUpdate()
		if last.Status != "todo" || !cmp.Equal(map[string]string{"retry-count": "1"}, last.Metadata) {
			t.Errorf("last = %+v", last)
		}
		requireContains(t, h.tasks.Notes()[0].Text, "Unresolved merge conflicts in: thing.ts.")
	})

	t.Run("pushes a clean merge of the base branch even when the agent changes nothing", func(t *testing.T) {
		h := setupConflicting(t, []string{})
		agentEditing(h, nil, false)
		h.mustRun()
		requireContains(t, h.prompt(0).UserPrompt, "without conflicts")
		if len(h.git.pushes) != 1 || h.lastUpdate().Status != "done" {
			t.Errorf("pushes %+v last %+v", h.git.pushes, h.lastUpdate())
		}
		requireContains(t, h.out.String(), "no changes, pushing the merge of main")
	})
}

func TestThreadReplies(t *testing.T) {
	replies := obj{"threadReplies": []obj{{"thread": "T1", "body": "Added the null check."}, {"thread": "T9", "body": "No such thread."}}}
	setupFollowUp := func(t *testing.T, extra obj) *harness {
		cfg := obj{"input": "task", "worktree": true}
		for k, v := range extra {
			cfg[k] = v
		}
		h := setup(t, project{agent: cfg})
		seedDoneTaskReopened(h)
		mockPR(h, "open", "mergeable")
		h.platform.threads = threads
		return h
	}

	t.Run("asks for replies by thread label", func(t *testing.T) {
		h := setupFollowUp(t, nil)
		agentEditing(h, nil, true)
		h.mustRun()
		requireContains(t, h.prompt(0).UserPrompt, "### T1 · `src/thing.ts:12`", `"threadReplies"`)
	})

	t.Run("posts annotated replies to known threads after pushing, and notes the count", func(t *testing.T) {
		h := setupFollowUp(t, nil)
		agentEditing(h, replies, true)
		h.mustRun()
		want := []replyCall{{Number: 7, ThreadID: "PRRT_null", Body: "🤖 **vteam test-agent:** Added the null check.\n\nFixed in abc1234."}}
		if diff := cmp.Diff(want, h.platform.replies); diff != "" {
			t.Error(diff)
		}
		if diff := cmp.Diff([]string{"push", "reply", "reply"}[:2], h.git.order[:2]); diff != "" {
			t.Errorf("order: %s", diff)
		}
		if len(h.platform.resolved) != 0 {
			t.Error("resolved without resolveThreads")
		}
		requireContains(t, h.tasks.Notes()[0].Text, "Replied to 1 of 2 review threads.")
		requireContains(t, h.out.String(), "skipping reply to unknown review thread T9")
	})

	t.Run("resolves replied threads when resolveThreads is on", func(t *testing.T) {
		h := setupFollowUp(t, obj{"resolveThreads": true})
		agentEditing(h, replies, true)
		h.mustRun()
		if diff := cmp.Diff([]string{"PRRT_null"}, h.platform.resolved); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("posts no replies when nothing was committed", func(t *testing.T) {
		h := setupFollowUp(t, obj{"resolveThreads": true})
		agentEditing(h, replies, false)
		h.mustRun()
		if len(h.git.pushes)+len(h.platform.replies)+len(h.platform.resolved) != 0 {
			t.Error("pushed or replied")
		}
	})

	t.Run("finishes the task when a reply cannot be posted", func(t *testing.T) {
		h := setupFollowUp(t, obj{"resolveThreads": true})
		h.platform.replyErr = errors.New("gh: forbidden")
		agentEditing(h, replies, true)
		h.mustRun()
		if len(h.platform.resolved) != 0 || h.lastUpdate().Status != "done" {
			t.Errorf("resolved %v last %+v", h.platform.resolved, h.lastUpdate())
		}
		requireContains(t, h.tasks.Notes()[0].Text, "Replied to 0 of 2 review threads.")
	})
}

func TestPRsOpenedWithoutATask(t *testing.T) {
	setupSelfDirected := func(t *testing.T) *harness {
		return setup(t, project{agent: obj{"worktree": true, "autoPR": true, "prCreateLabels": []string{"vteam"}}})
	}

	t.Run("records a done task with the PR so the merge ensurer tracks it", func(t *testing.T) {
		h := setupSelfDirected(t)
		agentEditing(h, nil, true)
		h.mustRun()
		want := []taskstest.Created{{Ref: "VTM-1", Body: model.TaskContentBody{Title: "guard null email", Severity: "low", Description: "Review follow-up.", Files: []string{"thing.ts"}},
			FoundBy: "test-agent", Labels: []string{"vteam-test-agent"}}}
		if diff := cmp.Diff(want, h.tasks.Created()); diff != "" {
			t.Error(diff)
		}
		updates := h.tasks.Updates()
		wantUpdate := []taskstest.UpdateCall{{Ref: "VTM-1", Update: tasks.Update{Status: "done",
			Metadata:  map[string]string{"branch": "vteam/test-agent-run", "pr-url": "https://github.com/org/repo/pull/99"},
			AddLabels: []string{"vteam-test-agent"}}}}
		if diff := cmp.Diff(wantUpdate, updates, cmpopts.IgnoreMapEntries(func(k, _ string) bool { return k == "completed" })); diff != "" {
			t.Error(diff)
		}
		if updates[0].Update.Metadata["completed"] == "" {
			t.Error("completed missing")
		}
		if n := h.tasks.Notes()[0]; n.Ref != "VTM-1" || !strings.Contains(n.Text, "PR: https://github.com/org/repo/pull/99") {
			t.Errorf("note = %+v", n)
		}
		req := h.platform.created[0]
		if req.Title != "vteam: test-agent: guard null email" || !cmp.Equal([]string{"vteam"}, req.Labels) {
			t.Errorf("PR request = %+v", req)
		}
	})

	t.Run("records no task when PR creation fails", func(t *testing.T) {
		h := setupSelfDirected(t)
		h.platform.createErr = errors.New("gh failed")
		agentEditing(h, nil, true)
		h.mustRun()
		if len(h.tasks.Created()) != 0 {
			t.Error("task recorded")
		}
		requireContains(t, h.out.String(), "PR creation failed: gh failed", "the branch was pushed, create the PR manually")
	})

	t.Run("keeps the recorded task done when its note fails", func(t *testing.T) {
		h := setupSelfDirected(t)
		h.tasks.FailNotes = true
		agentEditing(h, nil, true)
		h.mustRun()
		if h.lastUpdate().Status != "done" {
			t.Errorf("last = %+v", h.lastUpdate())
		}
	})

	t.Run("warns and keeps the run when recording the task fails", func(t *testing.T) {
		h := setupSelfDirected(t)
		h.tasks.FailCreate = true
		agentEditing(h, nil, true)
		h.mustRun()
		requireContains(t, h.out.String(), "failed to record a task for https://github.com/org/repo/pull/99")
	})
}

func TestPRInput(t *testing.T) {
	setupPR := func(t *testing.T, extra obj) *harness {
		cfg := obj{"input": "pr", "worktree": true, "prTriggerLabel": "vteam:fix"}
		for k, v := range extra {
			cfg[k] = v
		}
		h := setup(t, project{agent: cfg})
		h.platform.reviewPR = &model.ReviewablePR{Number: 5, Title: "Feature", Branch: "feature", URL: "https://github.com/org/repo/pull/5"}
		h.platform.comments = []model.ReviewComment{{Author: "amy", Body: "Rename it.", Path: "a.ts", Line: 2}}
		return h
	}

	t.Run("checks out the PR, comments, removes the trigger label, and passes the PR to ON_FINISH", func(t *testing.T) {
		h := setupPR(t, nil)
		writeFile(t, filepath.Join(h.cwd, "vteam", "agents", "test-agent", "ON_FINISH.md"), "Notify.")
		agentEditing(h, nil, true)
		h.claude.thenText("hook ok")
		h.mustRun()
		requireContains(t, h.prompt(0).UserPrompt, "## Pull Request", "Repository: org/repo", "### amy on `a.ts:2`")
		if len(h.git.pushes) != 1 || h.git.pushes[0].Branch != "feature" {
			t.Errorf("pushes = %+v", h.git.pushes)
		}
		if diff := cmp.Diff([]string{"Addressed review feedback in `abc1234`."}, h.platform.posted); diff != "" {
			t.Error(diff)
		}
		if diff := cmp.Diff([]string{"vteam:fix"}, h.platform.unlabeled); diff != "" {
			t.Error(diff)
		}
		requireContains(t, h.prompt(1).UserPrompt, "## Reviewed PR", "- Number: #5")
	})

	t.Run("removes the trigger label for a read-only agent without pushing", func(t *testing.T) {
		h := setupPR(t, obj{"readOnly": true})
		agentEditing(h, nil, true)
		h.mustRun()
		if len(h.git.pushes) != 0 || !cmp.Equal([]string{"vteam:fix"}, h.platform.unlabeled) {
			t.Errorf("pushes %v unlabeled %v", h.git.pushes, h.platform.unlabeled)
		}
		requireContains(t, h.out.String(), "read-only agent, nothing committed")
	})

	t.Run("does nothing when no PR is labelled", func(t *testing.T) {
		h := setupPR(t, nil)
		h.platform.reviewPR = nil
		h.mustRun()
		if len(h.claude.Calls()) != 0 {
			t.Error("agent ran")
		}
		requireContains(t, h.out.String(), "no PRs need a review response, nothing to do")
	})
}

func TestRunWritesState(t *testing.T) {
	h := setup(t, project{agent: obj{"output": "task"}})
	h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Found.", "content": obj{"type": "task", "body": obj{
		"title": "T", "severity": "low", "description": "D", "files": []string{"a"}}}}))
	h.mustRun()
	entries, _ := os.ReadDir(filepath.Join(h.cwd, "vteam", ".runs"))
	if len(entries) != 1 {
		t.Fatalf("run states = %v", entries)
	}
	data, _ := os.ReadFile(filepath.Join(h.cwd, "vteam", ".runs", entries[0].Name()))
	var state model.RunState
	_ = json.Unmarshal(data, &state)
	if state.Status != model.RunCompleted || !cmp.Equal([]string{"VTM-1"}, state.TasksCreated) || state.ClaudeOutput == nil || state.CompletedAt == "" {
		t.Errorf("state = %+v", state)
	}
}

func TestRunFailsWhenTheAgentLockIsHeld(t *testing.T) {
	h := setup(t, project{})
	if err := os.MkdirAll(filepath.Join(LocksDir(h.cwd), "test-agent.lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(LocksDir(h.cwd), "test-agent.lock", "info.json"), jsonText(obj{"pid": os.Getpid(), "timestamp": time.Now().UnixMilli(), "agent": "x"}))
	d := h.deps()
	d.LockTimeout = 100 * time.Millisecond
	agent, _ := config.ResolveAgent("test-agent", h.cwd)
	if err := Run(context.Background(), d, agent, Options{}); !errors.Is(err, ErrFailed) {
		t.Errorf("err = %v", err)
	}
	requireContains(t, h.out.String(), "timeout acquiring lock")
}

func TestStop(t *testing.T) {
	worktreeTask := obj{"input": "task", "worktree": true, "autoPR": true}

	stopDuringClaude := func(h *harness, cause error) error {
		ctx, cancel := context.WithCancelCause(context.Background())
		t.Cleanup(func() { cancel(nil) })
		h.claude.then(func(claude.Options) claude.Result {
			cancel(cause)
			return claude.Result{}
		})
		agent, _ := config.ResolveAgent("test-agent", h.cwd)
		return Run(ctx, h.deps(), agent, Options{})
	}

	t.Run("returns the task to todo, notes the stop, removes the worktree, and skips the hooks", func(t *testing.T) {
		h := setup(t, project{agent: worktreeTask, onFinish: true})
		seedTask(h, model.Task{})
		err := stopDuringClaude(h, context.Canceled)
		if !errors.Is(err, log.ErrStopped) {
			t.Fatalf("err = %v", err)
		}
		if diff := cmp.Diff([]model.TaskStatus{"active", "todo"}, h.statuses()); diff != "" {
			t.Error(diff)
		}
		if notes := h.tasks.Notes(); len(notes) != 1 || notes[0].Text != "test-agent: stopped\n\nThe run was stopped before it finished." {
			t.Errorf("notes = %+v", notes)
		}
		if len(h.git.removed) != 1 {
			t.Errorf("removed = %v", h.git.removed)
		}
		if len(h.claude.Calls()) != 1 {
			t.Errorf("claude calls = %d, want only the agent", len(h.claude.Calls()))
		}
		requireContains(t, h.out.String(), "worktree removed", "! test-agent stopped after")
		requireNotContains(t, h.out.String(), "failed")
	})

	t.Run("keeps the worktree when stopped with ErrStopKeepWorktree", func(t *testing.T) {
		h := setup(t, project{agent: worktreeTask})
		seedTask(h, model.Task{})
		if err := stopDuringClaude(h, ErrStopKeepWorktree); !errors.Is(err, log.ErrStopped) {
			t.Fatalf("err = %v", err)
		}
		if len(h.git.removed) != 0 {
			t.Errorf("removed = %v", h.git.removed)
		}
		requireContains(t, h.out.String(), "worktree kept, `vteam clean` removes it", ".worktrees")
		if diff := cmp.Diff([]model.TaskStatus{"active", "todo"}, h.statuses()); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("writes a stopped run state", func(t *testing.T) {
		h := setup(t, project{})
		_ = stopDuringClaude(h, context.Canceled)
		entries, _ := os.ReadDir(filepath.Join(h.cwd, "vteam", ".runs"))
		if len(entries) != 1 {
			t.Fatalf("run states = %v", entries)
		}
		data, _ := os.ReadFile(filepath.Join(h.cwd, "vteam", ".runs", entries[0].Name()))
		var state model.RunState
		_ = json.Unmarshal(data, &state)
		if state.Status != model.RunStopped {
			t.Errorf("state = %+v", state)
		}
	})

	t.Run("finishes publishing when the stop arrives after Claude answered", func(t *testing.T) {
		h := setup(t, project{agent: worktreeTask, onFinish: true})
		seedTask(h, model.Task{})
		agentEditing(h, nil, true)
		ctx, cancel := context.WithCancelCause(context.Background())
		t.Cleanup(func() { cancel(nil) })
		h.git.onPush = func() { cancel(context.Canceled) }
		agent, _ := config.ResolveAgent("test-agent", h.cwd)
		if err := Run(ctx, h.deps(), agent, Options{}); err != nil {
			t.Fatalf("err = %v\n%s", err, h.out.String())
		}
		if len(h.git.pushes) != 1 || len(h.platform.created) != 1 {
			t.Errorf("pushes = %v, PRs = %v", h.git.pushes, h.platform.created)
		}
		if h.lastUpdate().Status != "done" {
			t.Errorf("last update = %+v", h.lastUpdate())
		}
		if len(h.claude.Calls()) != 1 {
			t.Errorf("claude calls = %d, the on-finish hook should be skipped", len(h.claude.Calls()))
		}
		if len(h.git.removed) != 1 {
			t.Errorf("removed = %v", h.git.removed)
		}
	})
}

func TestNotifications(t *testing.T) {
	finding := func(title string) obj {
		return obj{"title": title, "severity": "medium", "description": "d", "files": []string{"a.go"}}
	}

	t.Run("announces a task created from a finding", func(t *testing.T) {
		h := setup(t, project{agent: obj{"output": "task"}})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Found.", "content": obj{"type": "task", "body": finding("Missing error handling")}}))
		h.mustRun()
		want := []notification{{"vteam test-agent: new task", "VTM-1 Missing error handling"}}
		if diff := cmp.Diff(want, h.notifier.Sent()); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("stays quiet when the agent found nothing", func(t *testing.T) {
		h := setup(t, project{})
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Done.", "content": obj{"type": "generic", "body": "All good."}}))
		h.mustRun()
		if sent := h.notifier.Sent(); len(sent) != 0 {
			t.Errorf("sent = %v", sent)
		}
	})

	t.Run("announces a PR opened for a task", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task", "worktree": true, "autoPR": true}})
		h.tasks.Add(model.Task{Ref: "VTM-NEW1", Title: "Fix the thing", Status: "todo", Severity: "high", Description: "It is broken.", CreatedAt: "2026-10-01T00:00:00Z"})
		agentEditing(h, nil, true)
		h.mustRun()
		want := []notification{{"vteam test-agent: PR created", "VTM-NEW1 Fix the thing"}}
		if diff := cmp.Diff(want, h.notifier.Sent()); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("announces a PR opened without a task", func(t *testing.T) {
		h := setup(t, project{agent: obj{"worktree": true, "autoPR": true}})
		agentEditing(h, nil, true)
		h.mustRun()
		sent := h.notifier.Sent()
		if diff := cmp.Diff([]notification{{"vteam test-agent: PR created", "VTM-1 guard null email"}}, sent); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("announces a task moved to done on an existing PR", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task", "worktree": true, "autoPR": true}})
		seedDoneTaskReopened(h)
		mockPR(h, "open", "mergeable")
		agentEditing(h, nil, true)
		h.mustRun()
		sent := h.notifier.Sent()
		if len(sent) != 1 || sent[0].Title != "vteam test-agent: task done" || !strings.HasPrefix(sent[0].Message, "VTM-") {
			t.Errorf("sent = %v", sent)
		}
	})

	t.Run("stays quiet when nothing was pushed", func(t *testing.T) {
		h := setup(t, project{agent: obj{"input": "task", "worktree": true, "autoPR": true}})
		h.tasks.Add(model.Task{Ref: "VTM-NEW1", Title: "Fix the thing", Status: "todo", Severity: "high", Description: "It is broken.", CreatedAt: "2026-10-01T00:00:00Z"})
		agentEditing(h, nil, false)
		h.mustRun()
		if sent := h.notifier.Sent(); len(sent) != 0 {
			t.Errorf("sent = %v", sent)
		}
	})

	t.Run("a failing notifier only warns", func(t *testing.T) {
		h := setup(t, project{agent: obj{"output": "task"}})
		h.notifier.err = errors.New("no display")
		h.claude.thenText(jsonText(obj{"status": "completed", "summary": "Found.", "content": obj{"type": "task", "body": finding("Stale lock")}}))
		h.mustRun()
		requireContains(t, h.out.String(), "failed to send notification: no display")
	})
}
