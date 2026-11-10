package status

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/runstate"
	"github.com/leodnlv/vteam/internal/session"
	"github.com/leodnlv/vteam/internal/tasks"
	"github.com/leodnlv/vteam/internal/tasks/taskstest"
)

var pipelineNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) string {
	return pipelineNow.Add(-d).Format(time.RFC3339)
}

func note(body string, at time.Duration) model.TaskNote {
	return model.TaskNote{Body: body, CreatedAt: ago(at)}
}

func pipelineFixture() pipelineData {
	const pr = "https://github.com/o/r/pull/"
	return pipelineData{
		maxRetries: 3,
		tasks: []model.Task{
			{Ref: "T-INBOX", Title: "fresh finding", Status: model.StatusInbox, CreatedAt: ago(2 * time.Hour),
				Metadata: map[string]string{model.MetaFoundBy: "improver"}},
			{Ref: "T-OPEN", Title: "fix in review", Status: model.StatusDone, CreatedAt: ago(10 * time.Hour),
				Metadata: map[string]string{model.MetaFoundBy: "improver", model.MetaPRURL: pr + "1"},
				Notes: []model.TaskNote{
					note("coder: completed\n\nfixed it\n\n"+tasks.PRParagraph(pr+"1"), 5*time.Hour),
					note(tasks.NoteHeadline("merge-ensurer", "reopened")+"\n\nPR has the label", 3*time.Hour),
					note("coder: completed\n\napplied review\n\n"+tasks.PRParagraph(pr+"1"), 1*time.Hour),
				}},
			{Ref: "T-MERGED", Title: "old fix", Status: model.StatusDone, CreatedAt: ago(72 * time.Hour),
				Metadata: map[string]string{model.MetaFoundBy: "security-analyst", model.MetaPRURL: pr + "2", model.MetaMerged: "true"},
				Notes:    []model.TaskNote{note("coder: completed\n\n"+tasks.PRParagraph(pr+"2"), 48*time.Hour)}},
			{Ref: "T-CLOSED", Title: "rejected PR", Status: model.StatusCanceled, CreatedAt: ago(20 * time.Hour),
				Metadata: map[string]string{model.MetaFoundBy: "improver", model.MetaPRURL: pr + "3"},
				Notes: []model.TaskNote{
					note("coder: completed\n\n"+tasks.PRParagraph(pr+"3"), 18*time.Hour),
					note(tasks.NoteHeadline("merge-ensurer", "canceled")+"\n\nPR was closed", 4*time.Hour),
				}},
			{Ref: "T-RECEIPT", Title: "add tests", Status: model.StatusDone, CreatedAt: ago(6 * time.Hour),
				Metadata: map[string]string{model.MetaFoundBy: "test-writer", model.MetaPRURL: pr + "4", model.MetaMerged: "true"},
				Notes:    []model.TaskNote{note("test-writer: completed\n\n"+tasks.PRParagraph(pr+"4"), 6*time.Hour)}},
			{Ref: "T-STUCK", Title: "keeps failing", Status: model.StatusTodo, RetryCount: 3, CreatedAt: ago(30 * time.Hour)},
			{Ref: "T-TODO", Title: "queued", Status: model.StatusTodo, RetryCount: 1, CreatedAt: ago(30 * time.Hour)},
		},
		ready: []model.Task{{Ref: "T-STUCK", RetryCount: 3}, {Ref: "T-TODO", RetryCount: 1}},
		runs: []model.RunState{
			{TaskRef: "T-OPEN", Status: model.RunCompleted, CommitSHA: "a", StartedAt: ago(5*time.Hour + 20*time.Minute), CompletedAt: ago(5 * time.Hour)},
			{TaskRef: "T-OPEN", Status: model.RunCompleted, CommitSHA: "b", StartedAt: ago(time.Hour + 10*time.Minute), CompletedAt: ago(time.Hour)},
			{TaskRef: "T-TODO", Status: model.RunFailed, StartedAt: ago(2 * time.Hour)},
			{TaskRef: "T-MERGED", Status: model.RunCompleted, CommitSHA: "c", StartedAt: ago(49 * time.Hour), CompletedAt: ago(48 * time.Hour)},
			{Agent: "improver", Status: model.RunCompleted, StartedAt: ago(time.Hour)},
		},
		sessions: []session.Session{{StartedAt: ago(30 * time.Minute)}, {StartedAt: ago(50 * time.Hour)}},
	}
}

// field returns the whitespace-separated fields of the first line whose text starts with prefix.
func field(t *testing.T, lines []string, prefix string) []string {
	t.Helper()
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			return strings.Fields(strings.TrimPrefix(strings.TrimSpace(l), prefix))
		}
	}
	t.Fatalf("no line starts with %q in:\n%s", prefix, strings.Join(lines, "\n"))
	return nil
}

func activityCount(t *testing.T, lines []string, name string) int {
	t.Helper()
	n, err := strconv.Atoi(field(t, lines, name)[0])
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return n
}

func TestPipelineCountsTheLast24Hours(t *testing.T) {
	lines := renderPipeline(pipelineFixture(), 24*time.Hour, pipelineNow)
	out := strings.Join(lines, "\n")

	if !strings.Contains(out, "Tasks created · last 24h (4)") {
		t.Errorf("want 4 tasks created in the window:\n%s", out)
	}
	for name, want := range map[string]int{"created": 4, "picked": 3, "PR opened": 3, "sent back": 1, "PR closed": 1, "sessions": 1} {
		if got := activityCount(t, lines, name); got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	if !strings.Contains(out, "1 PR follow-up(s)") {
		t.Errorf("the second T-OPEN run started after its PR, so it is a follow-up:\n%s", out)
	}
}

func TestPipelineLeadTimes(t *testing.T) {
	lines := renderPipeline(pipelineFixture(), 24*time.Hour, pipelineNow)

	toPR := field(t, lines, "created → first PR")
	if got := strings.Join(toPR[:5], " "); got != "5h 0m 5h 0m 2" {
		t.Errorf("created → first PR = %q, want T-OPEN (5h) and T-CLOSED (2h), without the receipt task", got)
	}
	work := field(t, lines, "picked → pushed")
	if work[0] != "20m" || work[1] != "20m" || work[2] != "2" {
		t.Errorf("picked → pushed = %v, want 20m, 20m over the 2 pushing runs in the window", work)
	}
	inbox := field(t, lines, "waiting in inbox")
	if strings.Join(inbox[:4], " ") != "2h 0m 2h 0m" {
		t.Errorf("waiting in inbox = %v", inbox)
	}
	review := field(t, lines, "open PRs waiting")
	if strings.Join(review[:2], " ") != "5h 0m" {
		t.Errorf("open PR age counts from the first PR note: %v", review)
	}
}

func TestPipelineWaitingAndQueue(t *testing.T) {
	out := strings.Join(renderPipeline(pipelineFixture(), 24*time.Hour, pipelineNow), "\n")

	for _, want := range []string{
		"inbox 1 · todo 2 (1 ready) · active 0 · PR open 1 · at max retries 1",
		"retries 0×0 · 1×1 · 2×0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Waiting on you") {
		t.Errorf("Waiting on you belongs to the status sections, not the pipeline:\n%s", out)
	}
}

func TestWaitingTasksAreOldestFirstAndCapped(t *testing.T) {
	all := pipelineFixture().tasks
	for i := range 5 {
		all = append(all, model.Task{Ref: fmt.Sprintf("T-NEW%d", i), Title: "newer", Status: model.StatusInbox, Severity: model.SeverityLow, CreatedAt: ago(time.Duration(i+1) * time.Minute)})
	}
	all = append(all, model.Task{Ref: "T-OLD", Title: "stale finding", Status: model.StatusInbox, Severity: model.SeverityHigh, CreatedAt: ago(50 * time.Hour)})
	lines := renderWaitingTasks(all, pipelineNow)

	want := []string{
		"  ! triage  2d 2h    T-OLD [high] stale finding  by hand",
		"  • review  5h 0m    T-OPEN [] fix in review  improver",
		"  • triage  2h 0m    T-INBOX [] fresh finding  improver",
		"  • triage  5m       T-NEW4 [low] newer  by hand",
		"  • triage  4m       T-NEW3 [low] newer  by hand",
		"  · and 3 more",
	}
	if diff := cmp.Diff(want, lines); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestPipelineSevenDays(t *testing.T) {
	lines := renderPipeline(pipelineFixture(), 7*24*time.Hour, pipelineNow)
	out := strings.Join(lines, "\n")

	if !strings.Contains(out, "Tasks created · last 7d (7)") {
		t.Errorf("want every task in 7 days:\n%s", out)
	}
	if got := activityCount(t, lines, "sessions"); got != 2 {
		t.Errorf("sessions = %d, want 2", got)
	}
	for _, want := range []string{"merged 2", "PR open 1", "inbox 1", "PR closed 1", "todo 2", "57% reached a PR · 29% merged"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestPipelineWithNoTasks(t *testing.T) {
	out := strings.Join(renderPipeline(pipelineData{maxRetries: 3}, 24*time.Hour, pipelineNow), "\n")

	if !strings.Contains(out, "no tasks created") {
		t.Errorf("want a placeholder:\n%s", out)
	}
	if !strings.Contains(out, "—         —         0 PRs") {
		t.Errorf("empty lead times show dashes:\n%s", out)
	}
}

func TestStackedBarKeepsWidthAndShowsEveryStage(t *testing.T) {
	counts := make([]int, len(stages))
	counts[stageMerged], counts[stageInbox], counts[stageRejected] = 98, 1, 1
	bar := []rune(stackedBar(counts, 20))
	if len(bar) != 20 {
		t.Fatalf("width = %d, want 20", len(bar))
	}
	if bar[len(bar)-1] != '░' || !strings.ContainsRune(string(bar), '█') {
		t.Errorf("bar = %q, want small stages kept at one cell", string(bar))
	}
}

func TestWindowLabel(t *testing.T) {
	for w, want := range map[time.Duration]string{24 * time.Hour: "24h", 7 * 24 * time.Hour: "7d", 12 * time.Hour: "12h"} {
		if got := windowLabel(w); got != want {
			t.Errorf("windowLabel(%v) = %q, want %q", w, got, want)
		}
	}
}

func TestRenderAddsThePipelineOnlyWithAWindow(t *testing.T) {
	cwd := t.TempDir()
	agent(t, cwd, "improver", `{"output":"task"}`)
	created := model.Task{Ref: "T-1", Title: "new", Status: model.StatusInbox, CreatedAt: ago(time.Hour), Metadata: map[string]string{}}
	write(t, filepath.Join(runstate.RunsDir(cwd), "r1.json"), `{"runId":"r1","agent":"coder","taskRef":"T-1","status":"failed","startedAt":"`+ago(30*time.Minute)+`"}`)

	plain, err := Render(context.Background(), deps(cwd, taskstest.New(created), pipelineNow, nil))
	if err != nil {
		t.Fatal(err)
	}
	if out := strings.Join(plain, "\n"); strings.Contains(out, "Tasks created") {
		t.Errorf("vteam status should not show the pipeline:\n%s", out)
	}

	d := deps(cwd, taskstest.New(created), pipelineNow, nil)
	d.Pipeline = 24 * time.Hour
	lines, err := Render(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if out := strings.Join(lines, "\n"); !strings.Contains(out, "Tasks created · last 24h (1)") || strings.Index(out, "Tasks created") < strings.Index(out, "Waiting on you") {
		t.Errorf("want the pipeline after the status sections:\n%s", out)
	}
	if got := activityCount(t, lines, "picked"); got != 1 {
		t.Errorf("picked = %d, want the run read from vteam/.runs", got)
	}
}
