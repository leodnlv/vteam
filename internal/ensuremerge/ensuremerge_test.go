package ensuremerge

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/platform"
	"github.com/leodnlv/vteam/internal/tasks"
	"github.com/leodnlv/vteam/internal/tasks/taskstest"
)

func TestMain(m *testing.M) {
	os.Setenv("NO_COLOR", "1")
	os.Exit(m.Run())
}

const prURL = "https://github.com/org/repo/pull/7"

var cfg = &config.Config{BaseBranch: "main", Platform: "github", Tasks: config.Tasks{MaxRetries: 3, Project: "app", Label: "vteam"},
	MergeEnsurer: config.MergeEnsurer{Label: "vteam:changes-requested"}}

type fakePlatform struct {
	platform.Platform
	calls     *[]string
	pr        func(url string) (model.PullRequestInfo, error)
	removeErr error
	lookups   []string
	removed   []string
}

func (f *fakePlatform) PullRequest(_ context.Context, url string) (model.PullRequestInfo, error) {
	f.lookups = append(f.lookups, url)
	return f.pr(url)
}

func (f *fakePlatform) RemoveLabel(_ context.Context, _ int, label string) error {
	*f.calls = append(*f.calls, "removeLabel")
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, label)
	return nil
}

func pr(mod func(*model.PullRequestInfo)) func(string) (model.PullRequestInfo, error) {
	return func(string) (model.PullRequestInfo, error) {
		p := model.PullRequestInfo{
			ReviewablePR: model.ReviewablePR{Number: 7, Title: "vteam: coder: Fix the thing", Branch: "vteam/fix-the-thing", URL: prURL},
			State:        "open", BaseBranch: "main", Labels: []string{}, Mergeable: "mergeable", Review: "none",
		}
		if mod != nil {
			mod(&p)
		}
		return p, nil
	}
}

func doneTask(ref string, meta map[string]string) model.Task {
	return model.Task{Ref: ref, Title: "Task " + ref, Status: "done", Severity: "medium", Metadata: meta, CreatedAt: "2026-10-01T00:00:00Z"}
}

type harness struct {
	tasks    *taskstest.Fake
	platform *fakePlatform
	out      bytes.Buffer
	calls    []string
}

func newHarness(prFn func(string) (model.PullRequestInfo, error), ts ...model.Task) *harness {
	h := &harness{tasks: taskstest.New(ts...)}
	h.platform = &fakePlatform{pr: prFn, calls: &h.calls}
	h.tasks.FailUpdate = func(string, tasks.Update) error { h.calls = append(h.calls, "update"); return nil }
	return h
}

func (h *harness) sweep(t *testing.T) Result {
	t.Helper()
	l := log.New(&h.out, &h.out)
	res, err := Sweep(context.Background(), Deps{Config: cfg, Log: l, Tasks: h.tasks, Platform: h.platform})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func refs(ts []model.Task) []string {
	out := []string{}
	for _, t := range ts {
		out = append(out, t.Ref)
	}
	return out
}

func TestSweep(t *testing.T) {
	t.Run("only looks at done tasks with a PR that is not yet recorded as merged", func(t *testing.T) {
		h := newHarness(pr(nil), doneTask("A", map[string]string{"pr-url": prURL}), doneTask("B", nil),
			doneTask("C", map[string]string{"pr-url": prURL, "merged": "true"}))
		res := h.sweep(t)
		if diff := cmp.Diff([]string{prURL}, h.platform.lookups); diff != "" {
			t.Error(diff)
		}
		if diff := cmp.Diff([]string{"A"}, refs(res.Waiting)); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("leaves the task done when the PR is open, unlabelled, and mergeable", func(t *testing.T) {
		h := newHarness(pr(func(p *model.PullRequestInfo) { p.Labels = []string{"vteam"} }), doneTask("A", map[string]string{"pr-url": prURL}))
		h.sweep(t)
		if len(h.tasks.Updates())+len(h.tasks.Notes())+len(h.platform.removed) != 0 {
			t.Error("changed something")
		}
	})

	t.Run("logs why a task is still waiting", func(t *testing.T) {
		h := newHarness(pr(func(p *model.PullRequestInfo) { p.Mergeable = "unknown"; p.Review = "approved" }), doneTask("A", map[string]string{"pr-url": prURL}))
		h.sweep(t)
		out := h.out.String()
		for _, want := range []string{"• A Task A\n", "PR        #7 " + prURL,
			`state     open · approved · no "vteam:changes-requested" label · mergeability not computed yet`, "action    none, waiting"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in:\n%s", want, out)
			}
		}
	})

	t.Run("records merged PRs and never looks at them again", func(t *testing.T) {
		h := newHarness(pr(func(p *model.PullRequestInfo) { p.State = "merged" }), doneTask("A", map[string]string{"pr-url": prURL}))
		res := h.sweep(t)
		want := []taskstest.UpdateCall{{Ref: "A", Update: tasks.Update{Metadata: map[string]string{"merged": "true"}}}}
		if diff := cmp.Diff(want, h.tasks.Updates()); diff != "" || len(res.Merged) != 1 {
			t.Errorf("merged %v\n%s", res.Merged, diff)
		}
		if h.tasks.Task("A").Status != "done" {
			t.Error("merged task left done")
		}
	})

	t.Run("cancels the task and notes it when the PR was closed", func(t *testing.T) {
		h := newHarness(pr(func(p *model.PullRequestInfo) { p.State = "closed" }), doneTask("A", map[string]string{"pr-url": prURL}))
		res := h.sweep(t)
		if h.tasks.Task("A").Status != "canceled" || len(res.Canceled) != 1 {
			t.Errorf("task %+v", h.tasks.Task("A"))
		}
		if diff := cmp.Diff([]taskstest.Note{{Ref: "A", Text: "merge-ensurer: canceled\n\nPR " + prURL + " was closed without merging."}}, h.tasks.Notes()); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("removes the label before reopening a task whose PR has it", func(t *testing.T) {
		h := newHarness(pr(func(p *model.PullRequestInfo) {
			p.Labels = []string{"vteam:changes-requested"}
			p.Review = "changes_requested"
		}),
			doneTask("A", map[string]string{"pr-url": prURL, "retry-count": "2"}))
		res := h.sweep(t)
		if diff := cmp.Diff([]string{"removeLabel", "update"}, h.calls); diff != "" {
			t.Error(diff)
		}
		want := tasks.Update{Status: "todo", RemoveMetadata: []string{"retry-count"}}
		if diff := cmp.Diff(want, h.tasks.Updates()[0].Update); diff != "" || len(res.Reopened) != 1 {
			t.Error(diff)
		}
		if note := h.tasks.Notes()[0].Text; note != "merge-ensurer: reopened\n\nPR "+prURL+` has the "vteam:changes-requested" label: apply its open review threads.` {
			t.Errorf("note = %q", note)
		}
		if !strings.Contains(h.out.String(), `state     open · changes requested · has the "vteam:changes-requested" label`) {
			t.Errorf("output:\n%s", h.out.String())
		}
	})

	t.Run("keeps the task done when the label cannot be removed", func(t *testing.T) {
		h := newHarness(pr(func(p *model.PullRequestInfo) { p.Labels = []string{"vteam:changes-requested"} }), doneTask("A", map[string]string{"pr-url": prURL}))
		h.platform.removeErr = errors.New("gh: forbidden")
		res := h.sweep(t)
		if len(h.tasks.Updates()) != 0 || len(res.Failed) != 1 {
			t.Errorf("updates %v failed %v", h.tasks.Updates(), res.Failed)
		}
		if !strings.Contains(h.out.String(), "error     ") || !strings.Contains(h.out.String(), "gh: forbidden") {
			t.Errorf("output:\n%s", h.out.String())
		}
	})

	t.Run("reopens a task whose PR conflicts with its base branch", func(t *testing.T) {
		h := newHarness(pr(func(p *model.PullRequestInfo) { p.Mergeable = "conflicting" }), doneTask("A", map[string]string{"pr-url": prURL}))
		h.sweep(t)
		if h.tasks.Task("A").Status != "todo" || len(h.platform.removed) != 0 {
			t.Errorf("task %+v removed %v", h.tasks.Task("A"), h.platform.removed)
		}
		if note := h.tasks.Notes()[0].Text; !strings.Contains(note, "conflicts with main: merge main and resolve the conflicts.") {
			t.Errorf("note = %q", note)
		}
	})

	t.Run("notes both reasons when the PR is labelled and conflicting", func(t *testing.T) {
		h := newHarness(pr(func(p *model.PullRequestInfo) {
			p.Mergeable = "conflicting"
			p.Labels = []string{"vteam:changes-requested"}
		}),
			doneTask("A", map[string]string{"pr-url": prURL}))
		h.sweep(t)
		note := h.tasks.Notes()[0].Text
		if !strings.Contains(note, "label: apply") || !strings.Contains(note, "conflicts with main") {
			t.Errorf("note = %q", note)
		}
	})

	t.Run("keeps going after a task fails", func(t *testing.T) {
		h := newHarness(func(url string) (model.PullRequestInfo, error) {
			if strings.HasSuffix(url, "/1") {
				return model.PullRequestInfo{}, errors.New("gh: not found")
			}
			return pr(func(p *model.PullRequestInfo) { p.State = "merged" })(url)
		}, doneTask("A", map[string]string{"pr-url": "https://x/pull/1"}), doneTask("B", map[string]string{"pr-url": "https://x/pull/2"}))
		res := h.sweep(t)
		if !cmp.Equal([]string{"A"}, refs(res.Failed)) || !cmp.Equal([]string{"B"}, refs(res.Merged)) {
			t.Errorf("result %+v", res)
		}
	})

	t.Run("does not fail when a note cannot be written", func(t *testing.T) {
		h := newHarness(pr(func(p *model.PullRequestInfo) { p.State = "closed" }), doneTask("A", map[string]string{"pr-url": prURL}))
		h.tasks.FailNotes = true
		res := h.sweep(t)
		if len(res.Canceled) != 1 || !strings.Contains(h.out.String(), "failed to add note to task A") {
			t.Errorf("result %+v output:\n%s", res, h.out.String())
		}
	})

	t.Run("says when there is nothing to do", func(t *testing.T) {
		h := newHarness(pr(nil), doneTask("A", nil))
		h.sweep(t)
		if !strings.Contains(h.out.String(), "no done tasks with an unmerged PR, nothing to do") || strings.Contains(h.out.String(), "Pull requests") {
			t.Errorf("output:\n%s", h.out.String())
		}
	})
}

func TestLogSummary(t *testing.T) {
	var out bytes.Buffer
	l := log.New(&out, &out)
	LogSummary(l, Result{Merged: make([]model.Task, 2), Waiting: make([]model.Task, 1), Failed: make([]model.Task, 1)})
	if !strings.Contains(out.String(), "✓ merged 2 · canceled 0 · reopened 0 · waiting 1") || !strings.Contains(out.String(), "✗ 1 task(s) failed") {
		t.Errorf("output:\n%s", out.String())
	}
}
