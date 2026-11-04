package tasks

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/execx"
	"github.com/leodnlv/vteam/internal/model"
)

var testConfig = config.Tasks{MaxRetries: 3, Project: "app", Label: "vteam"}

var finding = model.TaskContentBody{
	Title:        "Null check missing",
	Severity:     model.SeverityCritical,
	Description:  "Crashes on empty input.",
	SuggestedFix: "Guard the input.",
	Files:        []string{"src/a.ts:10", "src/b.ts"},
}

func detail(ref string, overrides map[string]any) string {
	d := map[string]any{
		"task":        map[string]any{"ref": ref, "title": "Task " + ref, "status": "todo", "priority": "high", "created_at": "2026-10-01T00:00:00Z"},
		"description": "body",
		"metadata":    map[string]string{"retry-count": "2"},
		"notes":       []map[string]string{{"id": "N1", "body": "Tried once.", "created_at": "2026-10-02T00:00:00Z"}},
	}
	for k, v := range overrides {
		d[k] = v
	}
	data, _ := json.Marshal(d)
	return string(data)
}

func fakeAven(handler func(args []string) string) (*Aven, *execx.Fake) {
	fake := &execx.Fake{Handler: func(c execx.Cmd) (string, error) { return handler(c.Args), nil }}
	return &Aven{Config: testConfig, Exec: fake}, fake
}

func argsOf(f *execx.Fake) [][]string {
	var out [][]string
	for _, c := range f.Calls() {
		out = append(out, c.Args)
	}
	return out
}

func TestAvenCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("creates a labelled inbox task with metadata and returns its ref", func(t *testing.T) {
		a, fake := fakeAven(func(args []string) string {
			if args[0] == "add" {
				return "created APP-7KQ9 ref=7KQ9 project=app status=inbox priority=urgent title=\"x\"\n"
			}
			return ""
		})
		ref, err := a.Create(ctx, finding, "improver", nil)
		if err != nil || ref != "APP-7KQ9" {
			t.Fatalf("ref %q err %v", ref, err)
		}
		calls := fake.Calls()
		want := [][]string{
			{"label", "create", "vteam"},
			{"add", "Null check missing", "--project", "app", "--status", "inbox", "--priority", "urgent",
				"--label", "vteam", "--metadata", "found-by=improver", "--description-stdin"},
			{"edit", "APP-7KQ9", "--status", "inbox"},
		}
		if diff := cmp.Diff(want, argsOf(fake)); diff != "" {
			t.Error(diff)
		}
		if !strings.Contains(calls[1].Stdin, "## Suggested Fix\n\nGuard the input.") || !strings.Contains(calls[1].Stdin, "- `src/b.ts`") {
			t.Errorf("stdin = %q", calls[1].Stdin)
		}
	})

	t.Run("adds the agent's labels after the config label, without duplicates", func(t *testing.T) {
		a, fake := fakeAven(func(args []string) string {
			if args[0] == "add" {
				return "created APP-7KQ9 ref=7KQ9\n"
			}
			return ""
		})
		if _, err := a.Create(ctx, finding, "improver", []string{"vteam-improver", "vteam"}); err != nil {
			t.Fatal(err)
		}
		args := argsOf(fake)
		if diff := cmp.Diff([][]string{{"label", "create", "vteam"}, {"label", "create", "vteam-improver"}}, args[:2]); diff != "" {
			t.Error(diff)
		}
		var labels []string
		for i, arg := range args[2] {
			if i > 0 && args[2][i-1] == "--label" {
				labels = append(labels, arg)
			}
		}
		if diff := cmp.Diff([]string{"vteam", "vteam-improver"}, labels); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("fails when the created ref cannot be parsed", func(t *testing.T) {
		a, _ := fakeAven(func([]string) string { return "something unexpected" })
		if _, err := a.Create(ctx, finding, "x", nil); err == nil || !strings.Contains(err.Error(), "could not parse task ref") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestAvenList(t *testing.T) {
	ctx := context.Background()

	t.Run("lists every ready labelled task with its metadata", func(t *testing.T) {
		a, fake := fakeAven(func(args []string) string {
			if args[0] == "list" {
				return `[{"ref":"APP-0001"}]`
			}
			return detail(args[1], nil)
		})
		got, err := a.ListReady(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := [][]string{
			{"list", "--project", "app", "--label", "vteam", "--status", "todo", "--ready", "--json"},
			{"show", "APP-0001", "--full", "--json"},
		}
		if diff := cmp.Diff(want, argsOf(fake)); diff != "" {
			t.Error(diff)
		}
		wantTasks := []model.Task{{
			Ref: "APP-0001", Title: "Task APP-0001", Status: "todo", Severity: "high", RetryCount: 2,
			Metadata: map[string]string{"retry-count": "2"}, Description: "body",
			Notes: []model.TaskNote{{Body: "Tried once.", CreatedAt: "2026-10-02T00:00:00Z"}}, CreatedAt: "2026-10-01T00:00:00Z",
		}}
		if diff := cmp.Diff(wantTasks, got); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("lists done tasks", func(t *testing.T) {
		a, fake := fakeAven(func(args []string) string { return "[]" })
		if _, err := a.ListDone(ctx); err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff([]string{"list", "--project", "app", "--label", "vteam", "--status", "done", "--json"}, argsOf(fake)[0]); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("maps priority none and missing metadata to safe defaults", func(t *testing.T) {
		a, _ := fakeAven(func(args []string) string {
			if args[0] == "list" {
				return `[{"ref":"APP-0002"}]`
			}
			return detail(args[1], map[string]any{
				"task":     map[string]any{"ref": "APP-0002", "title": "Human task", "status": "todo", "priority": "none", "created_at": ""},
				"metadata": nil,
				"notes":    nil,
			})
		})
		got, err := a.ListReady(ctx)
		if err != nil {
			t.Fatal(err)
		}
		task := got[0]
		if task.Severity != "low" || task.RetryCount != 0 || len(task.Metadata) != 0 || task.Metadata == nil || len(task.Notes) != 0 {
			t.Errorf("task = %+v", task)
		}
	})

	t.Run("keeps aven's order while fetching details concurrently", func(t *testing.T) {
		var list []map[string]string
		for i := range 20 {
			list = append(list, map[string]string{"ref": string(rune('A' + i))})
		}
		listJSON, _ := json.Marshal(list)
		a, _ := fakeAven(func(args []string) string {
			if args[0] == "list" {
				return string(listJSON)
			}
			return detail(args[1], map[string]any{"task": map[string]any{"ref": args[1], "title": args[1], "status": "todo", "priority": "low"}})
		})
		idx, err := a.GetIndex(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i, task := range idx.All {
			if task.Ref != string(rune('A'+i)) {
				t.Fatalf("order broken at %d: %q", i, task.Ref)
			}
		}
	})

	t.Run("groups the index by status", func(t *testing.T) {
		a, _ := fakeAven(func(args []string) string {
			if args[0] == "list" {
				return `[{"ref":"A"},{"ref":"B"},{"ref":"C"}]`
			}
			status := "todo"
			if args[1] == "C" {
				status = "canceled"
			}
			return detail(args[1], map[string]any{"task": map[string]any{"ref": args[1], "title": args[1], "status": status, "priority": "low"}})
		})
		idx, err := a.GetIndex(ctx)
		if err != nil {
			t.Fatal(err)
		}
		titles := []string{}
		for _, task := range idx.All {
			titles = append(titles, task.Title)
		}
		if diff := cmp.Diff([]string{"A", "B", "C"}, titles); diff != "" {
			t.Error(diff)
		}
		if len(idx.ByStatus["todo"]) != 2 || idx.ByStatus["canceled"][0].Ref != "C" {
			t.Errorf("byStatus = %+v", idx.ByStatus)
		}
	})

	t.Run("returns no tasks when the project or label does not exist yet", func(t *testing.T) {
		a := &Aven{Config: testConfig, Exec: &execx.Fake{Handler: func(c execx.Cmd) (string, error) {
			return "", execx.Failure(c, "Error: error unknown-project input=app")
		}}}
		got, err := a.ListReady(ctx)
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("got %v err %v", got, err)
		}
	})

	t.Run("propagates other aven errors", func(t *testing.T) {
		a := &Aven{Config: testConfig, Exec: &execx.Fake{Handler: func(c execx.Cmd) (string, error) {
			return "", execx.Failure(c, "Error: database locked")
		}}}
		if _, err := a.GetIndex(ctx); err == nil || !strings.Contains(err.Error(), "database locked") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("propagates a failing show", func(t *testing.T) {
		a := &Aven{Config: testConfig, Exec: &execx.Fake{Handler: func(c execx.Cmd) (string, error) {
			if c.Args[0] == "list" {
				return `[{"ref":"A"}]`, nil
			}
			return "", execx.Failure(c, "no such task")
		}}}
		if _, err := a.GetIndex(ctx); err == nil || !strings.Contains(err.Error(), "no such task") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("explains a missing aven binary", func(t *testing.T) {
		a := &Aven{Config: testConfig, Exec: &execx.Fake{Handler: func(c execx.Cmd) (string, error) {
			return "", execx.ErrNotFound
		}}}
		if _, err := a.GetIndex(ctx); err == nil || !strings.Contains(err.Error(), "aven CLI not found on PATH") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestAvenUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("updates status and skips empty metadata", func(t *testing.T) {
		a, fake := fakeAven(func([]string) string { return "" })
		_ = a.Update(ctx, "APP-0001", Update{Status: "done", Metadata: map[string]string{"branch": "vteam/x", "pr-url": ""}})
		if diff := cmp.Diff([]string{"edit", "APP-0001", "--status", "done", "--metadata", "branch=vteam/x"}, argsOf(fake)[0]); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("writes metadata in key order", func(t *testing.T) {
		a, fake := fakeAven(func([]string) string { return "" })
		_ = a.Update(ctx, "APP-0001", Update{Metadata: map[string]string{"pr-url": "u", "branch": "b", "completed": "c"}})
		want := []string{"edit", "APP-0001", "--metadata", "branch=b", "--metadata", "completed=c", "--metadata", "pr-url=u"}
		if diff := cmp.Diff(want, argsOf(fake)[0]); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("creates and adds labels on update", func(t *testing.T) {
		a, fake := fakeAven(func([]string) string { return "" })
		_ = a.Update(ctx, "APP-0001", Update{Status: "done", AddLabels: []string{"vteam-coder"}})
		want := [][]string{{"label", "create", "vteam-coder"}, {"edit", "APP-0001", "--status", "done", "--label", "vteam-coder"}}
		if diff := cmp.Diff(want, argsOf(fake)); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("removes metadata keys", func(t *testing.T) {
		a, fake := fakeAven(func([]string) string { return "" })
		_ = a.Update(ctx, "APP-0001", Update{Status: "todo", RemoveMetadata: []string{"claimed-by", "claimed-pid"}})
		want := []string{"edit", "APP-0001", "--status", "todo", "--remove-metadata", "claimed-by", "--remove-metadata", "claimed-pid"}
		if diff := cmp.Diff(want, argsOf(fake)[0]); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("adds a note via stdin", func(t *testing.T) {
		a, fake := fakeAven(func([]string) string { return "" })
		_ = a.AddNote(ctx, "APP-0001", "line one\nline two")
		c := fake.Calls()[0]
		if diff := cmp.Diff([]string{"note", "APP-0001", "--stdin"}, c.Args); diff != "" || c.Stdin != "line one\nline two" {
			t.Errorf("call = %+v", c)
		}
	})

	t.Run("prefixes every command with the configured workspace", func(t *testing.T) {
		a, fake := fakeAven(func([]string) string { return "" })
		a.Config.Workspace = "work"
		_ = a.Update(ctx, "APP-0001", Update{Status: "active"})
		if diff := cmp.Diff([]string{"--workspace", "work", "edit", "APP-0001", "--status", "active"}, argsOf(fake)[0]); diff != "" {
			t.Error(diff)
		}
	})
}

func realAven(t *testing.T) (*Aven, func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("aven"); err != nil {
		t.Skip("aven is not installed")
	}
	db := filepath.Join(t.TempDir(), "aven.sqlite")
	a := &Aven{Config: testConfig, Exec: execx.OS{}, GlobalArgs: []string{"--db", db}}
	run := func(args ...string) string {
		out, err := execx.OS{}.Run(context.Background(), execx.Cmd{Name: "aven", Args: append([]string{"--db", db}, args...)})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	return a, run
}

func labelsOf(t *testing.T, run func(...string) string, ref string) []string {
	var shown struct{ Labels []string }
	if err := json.Unmarshal([]byte(run("show", ref, "--json")), &shown); err != nil {
		t.Fatal(err)
	}
	slices.Sort(shown.Labels)
	return shown.Labels
}

func TestAvenAgainstRealBinary(t *testing.T) {
	ctx := context.Background()

	t.Run("exposes found-by metadata on listed tasks", func(t *testing.T) {
		a, _ := realAven(t)
		ref, err := a.Create(ctx, finding, "improver", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = a.Update(ctx, ref, Update{Status: "todo"})
		ready, _ := a.ListReady(ctx)
		if len(ready) != 1 || ready[0].Ref != ref || ready[0].Metadata["found-by"] != "improver" {
			t.Errorf("ready = %+v", ready)
		}
	})

	t.Run("adds create and done labels to the aven task", func(t *testing.T) {
		a, run := realAven(t)
		ref, _ := a.Create(ctx, finding, "improver", []string{"vteam-improver"})
		_ = a.Update(ctx, ref, Update{Status: "done", AddLabels: []string{"vteam-coder"}})
		_ = a.Update(ctx, ref, Update{Status: "done", AddLabels: []string{"vteam-coder"}})
		if diff := cmp.Diff([]string{"vteam", "vteam-coder", "vteam-improver"}, labelsOf(t, run, ref)); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("normalizes labels the same way aven does", func(t *testing.T) {
		a, run := realAven(t)
		raw := "Bot_Name: Security.Analyst"
		ref, err := a.Create(ctx, finding, "improver", []string{raw})
		if err != nil {
			t.Fatal(err)
		}
		if labels := labelsOf(t, run, ref); !slices.Contains(labels, ToAvenLabel(raw)) {
			t.Errorf("labels = %v", labels)
		}
	})

	t.Run("round-trips a task through its lifecycle", func(t *testing.T) {
		a, _ := realAven(t)
		if ready, _ := a.ListReady(ctx); len(ready) != 0 {
			t.Fatalf("ready = %+v", ready)
		}
		ref, err := a.Create(ctx, finding, "improver", nil)
		if err != nil {
			t.Fatal(err)
		}
		if ready, _ := a.ListReady(ctx); len(ready) != 0 {
			t.Fatal("inbox task is ready")
		}
		idx, _ := a.GetIndex(ctx)
		if len(idx.ByStatus["inbox"]) != 1 || idx.ByStatus["inbox"][0].Ref != ref {
			t.Fatalf("inbox = %+v", idx.ByStatus["inbox"])
		}

		_ = a.Update(ctx, ref, Update{Status: "todo"})
		ready, _ := a.ListReady(ctx)
		if len(ready) != 1 {
			t.Fatalf("ready = %+v", ready)
		}
		r := ready[0]
		if r.Ref != ref || r.Title != "Null check missing" || r.Status != "todo" || r.Severity != "critical" || r.RetryCount != 0 {
			t.Errorf("ready = %+v", r)
		}
		if !strings.Contains(r.Description, "Crashes on empty input.") || !strings.Contains(r.Description, "- `src/b.ts`") {
			t.Errorf("description = %q", r.Description)
		}
		if _, ok := r.Metadata["files"]; ok {
			t.Error("files stored as metadata")
		}

		_ = a.Update(ctx, ref, Update{Status: "active", Metadata: map[string]string{"claimed-by": "run-1"}})
		if ready, _ := a.ListReady(ctx); len(ready) != 0 {
			t.Fatal("active task is ready")
		}

		_ = a.Update(ctx, ref, Update{Status: "todo", Metadata: map[string]string{"retry-count": "1"}, RemoveMetadata: []string{"claimed-by"}})
		ready, _ = a.ListReady(ctx)
		if ready[0].RetryCount != 1 {
			t.Errorf("retries = %d", ready[0].RetryCount)
		}
		if _, ok := ready[0].Metadata["claimed-by"]; ok {
			t.Error("claimed-by not removed")
		}

		_ = a.AddNote(ctx, ref, "coder: completed\n\nFixed it.")
		_ = a.Update(ctx, ref, Update{Status: "done", Metadata: map[string]string{"branch": "vteam/fix"}})
		idx, _ = a.GetIndex(ctx)
		if len(idx.ByStatus["done"]) != 1 || len(idx.All[0].Notes) != 1 || idx.All[0].Notes[0].Body != "coder: completed\n\nFixed it." || idx.All[0].Notes[0].CreatedAt == "" {
			t.Errorf("done = %+v", idx.All)
		}
	})
}
