package ensuremerge

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/session"
	"github.com/leodnlv/vteam/internal/tasks"
	"github.com/leodnlv/vteam/internal/tasks/taskstest"
	"github.com/leodnlv/vteam/internal/workmux"
)

type fakeWorkmux struct {
	workmux.Client
	list []workmux.Worktree
}

func (f *fakeWorkmux) List(context.Context, string) ([]workmux.Worktree, error) { return f.list, nil }

func activeTask(ref, runID string) model.Task {
	return model.Task{Ref: ref, Title: "Task " + ref, Status: "active", Severity: "medium", CreatedAt: "2026-10-01T00:00:00Z",
		Metadata: map[string]string{"claimed-by": runID, "claimed-host": "h", "claimed-session": runID}}
}

func saveSession(t *testing.T, cwd, runID, handle string, status session.Status, task *model.Task) {
	t.Helper()
	err := session.Save(cwd, session.Session{RunID: runID, Agent: "coder", Status: status, StartedAt: runID, Handle: handle,
		WorktreePath: "/w/" + handle, Task: task})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSweepAbandonsSessionsWhoseWindowWasClosed(t *testing.T) {
	cwd := t.TempDir()
	closed, open, stolen := activeTask("A", "run-1"), activeTask("B", "run-2"), activeTask("C", "other-run")
	saveSession(t, cwd, "run-1", "closed", session.Open, &closed)
	saveSession(t, cwd, "run-2", "open", session.Open, &open)
	saveSession(t, cwd, "run-3", "gone", session.Open, &model.Task{Ref: "C"})
	saveSession(t, cwd, "run-4", "scratch", session.Open, nil)
	saveSession(t, cwd, "run-5", "submitting", session.Submitting, &model.Task{Ref: "D"})
	fake := taskstest.New(closed, open, stolen)
	wm := &fakeWorkmux{list: []workmux.Worktree{{Handle: "closed"}, {Handle: "open", IsOpen: true}, {Handle: "scratch"}}}

	var out bytes.Buffer
	res, err := Sweep(context.Background(), Deps{Cwd: cwd, Config: cfg, Log: log.New(&out, &out), Tasks: fake, Platform: &fakePlatform{}, Workmux: wm})
	if err != nil {
		t.Fatal(err)
	}

	var handles []string
	for _, s := range res.Abandoned {
		handles = append(handles, s.Handle)
	}
	if diff := cmp.Diff([]string{"closed", "gone", "scratch"}, handles); diff != "" {
		t.Error(diff)
	}
	updates := fake.Updates()
	if len(updates) != 1 || updates[0].Ref != "A" || updates[0].Update.Status != "todo" {
		t.Fatalf("updates = %+v", updates)
	}
	if diff := cmp.Diff(tasks.SessionClaimKeys, updates[0].Update.RemoveMetadata); diff != "" {
		t.Error(diff)
	}
	notes := fake.Notes()
	if len(notes) != 1 || !strings.Contains(notes[0].Text, "coder: abandoned") || !strings.Contains(notes[0].Text, "/w/closed") {
		t.Errorf("notes = %+v", notes)
	}
	statuses := map[string]session.Status{}
	for _, s := range session.List(cwd) {
		statuses[s.Handle] = s.Status
	}
	want := map[string]session.Status{"closed": "abandoned", "open": "open", "gone": "abandoned", "scratch": "abandoned", "submitting": "submitting"}
	if diff := cmp.Diff(want, statuses); diff != "" {
		t.Error(diff)
	}
	for _, line := range []string{"Interactive sessions", "open coder session open", "task C no longer held by it", "abandoned 3 interactive session(s)"} {
		LogSummary(log.New(&out, &out), res)
		if !strings.Contains(out.String(), line) {
			t.Errorf("missing %q in:\n%s", line, out.String())
		}
	}
}

func TestSweepSkipsSessionsWithoutWorkmux(t *testing.T) {
	cwd := t.TempDir()
	saveSession(t, cwd, "run-1", "closed", session.Open, nil)
	var out bytes.Buffer
	res, err := Sweep(context.Background(), Deps{Cwd: cwd, Config: cfg, Log: log.New(&out, &out), Tasks: taskstest.New(), Platform: &fakePlatform{}})
	if err != nil || len(res.Abandoned) != 0 {
		t.Errorf("res %+v err %v", res, err)
	}
}
