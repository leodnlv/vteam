// Package ensuremerge moves done tasks through review: it records merges, cancels tasks whose PR was closed,
// and reopens tasks whose PR has the changes-requested label or conflicts with its base branch. It also marks
// interactive sessions whose window was closed as abandoned and releases their tasks.
package ensuremerge

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/leodnlv/vteam/internal/ansi"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/platform"
	"github.com/leodnlv/vteam/internal/session"
	"github.com/leodnlv/vteam/internal/tasks"
	"github.com/leodnlv/vteam/internal/workmux"
)

const Name = "merge-ensurer"

type Deps struct {
	Cwd      string
	Config   *config.Config
	Log      *log.Logger
	Tasks    tasks.Manager
	Platform platform.Platform
	Workmux  workmux.Client
}

type Result struct {
	Merged, Canceled, Reopened, Waiting, Failed []model.Task
	Abandoned                                   []session.Session
}

type outcome int

const (
	merged outcome = iota
	canceled
	reopened
	waiting
)

func Sweep(ctx context.Context, d Deps) (Result, error) {
	var res Result
	res.Abandoned = sweepSessions(ctx, d)
	if ctx.Err() != nil {
		return res, nil
	}
	done, err := d.Tasks.ListDone(ctx)
	if err != nil {
		return res, err
	}
	var pending []model.Task
	for _, t := range done {
		if t.Metadata[model.MetaPRURL] != "" && t.Metadata[model.MetaMerged] != "true" {
			pending = append(pending, t)
		}
	}
	if len(pending) == 0 {
		d.Log.Step("no done tasks with an unmerged PR, nothing to do")
		return res, nil
	}
	d.Log.Step(fmt.Sprintf("checking %d done task(s) with an unmerged PR", len(pending)))
	d.Log.Section("Pull requests")

	for _, t := range pending {
		if ctx.Err() != nil {
			d.Log.Warn("stopped before checking the remaining PRs")
			break
		}
		// One PR is handled in full even when a stop arrives, so a removed label always reopens its task.
		o, err := ensure(context.WithoutCancel(ctx), d, t)
		if err != nil {
			d.Log.Fail(fmt.Sprintf("%s %s", t.Ref, t.Title))
			d.Log.Field("PR", t.Metadata[model.MetaPRURL])
			d.Log.Field("error", err.Error())
			res.Failed = append(res.Failed, t)
			continue
		}
		switch o {
		case merged:
			res.Merged = append(res.Merged, t)
		case canceled:
			res.Canceled = append(res.Canceled, t)
		case reopened:
			res.Reopened = append(res.Reopened, t)
		case waiting:
			res.Waiting = append(res.Waiting, t)
		}
	}
	return res, nil
}

var reviewText = map[model.PRReviewStatus]string{
	model.ReviewApproved:         "approved",
	model.ReviewChangesRequested: "changes requested",
	model.ReviewRequired:         "awaiting approval",
	model.ReviewNone:             "no reviews yet",
}

var mergeabilityText = map[model.PRMergeability]string{
	model.Mergeable:           "no conflicts",
	model.Conflicting:         "conflicting",
	model.MergeabilityUnknown: "mergeability not computed yet",
}

func ensure(ctx context.Context, d Deps, t model.Task) (outcome, error) {
	pr, err := d.Platform.PullRequest(ctx, t.Metadata[model.MetaPRURL])
	if err != nil {
		return 0, err
	}
	label := d.Config.MergeEnsurer.Label

	switch pr.State {
	case model.PRMerged:
		if err := d.Tasks.Update(ctx, t.Ref, tasks.Update{Metadata: map[string]string{model.MetaMerged: "true"}}); err != nil {
			return 0, err
		}
		report(d.Log.OK, d.Log, t, pr, "merged", "recorded merged=true")
		return merged, nil
	case model.PRClosed:
		if err := d.Tasks.Update(ctx, t.Ref, tasks.Update{Status: model.StatusCanceled}); err != nil {
			return 0, err
		}
		addNote(ctx, d, t, tasks.NoteHeadline(Name, "canceled")+fmt.Sprintf("\n\nPR %s was closed without merging.", pr.URL))
		report(d.Log.Warn, d.Log, t, pr, "closed without merging", "task canceled")
		return canceled, nil
	}

	changesRequested := slices.Contains(pr.Labels, label)
	conflicting := pr.Mergeable == model.Conflicting
	if !changesRequested && !conflicting {
		state := []string{"open", reviewText[pr.Review], fmt.Sprintf("no %q label", label), mergeabilityText[pr.Mergeable]}
		report(d.Log.Step, d.Log, t, pr, strings.Join(state, " · "), "none, waiting")
		return waiting, nil
	}

	// Remove the label before reopening: if the task update then fails, the task stays done and the label
	// can be re-added, instead of the task reopening on every tick after each push.
	if changesRequested {
		if err := d.Platform.RemoveLabel(ctx, pr.Number, label); err != nil {
			return 0, err
		}
	}
	if err := d.Tasks.Update(ctx, t.Ref, tasks.Update{Status: model.StatusTodo, RemoveMetadata: []string{model.MetaRetryCount}}); err != nil {
		return 0, err
	}
	addNote(ctx, d, t, reopenNote(pr, label, changesRequested, conflicting))
	state := []string{"open", reviewText[pr.Review]}
	if changesRequested {
		state = append(state, fmt.Sprintf("has the %q label", label))
	}
	if conflicting {
		state = append(state, "conflicts with "+pr.BaseBranch)
	}
	report(d.Log.OK, d.Log, t, pr, strings.Join(state, " · "), "task moved to todo")
	return reopened, nil
}

func report(headline func(string), l *log.Logger, t model.Task, pr model.PullRequestInfo, state, action string) {
	headline(ansi.Bold(t.Ref) + " " + t.Title)
	l.Field("PR", fmt.Sprintf("#%d %s", pr.Number, pr.URL))
	l.Field("state", state)
	l.Field("action", action)
}

func reopenNote(pr model.PullRequestInfo, label string, changesRequested, conflicting bool) string {
	parts := []string{tasks.NoteHeadline(Name, "reopened")}
	if changesRequested {
		parts = append(parts, fmt.Sprintf("PR %s has the %q label: apply its open review threads.", pr.URL, label))
	}
	if conflicting {
		parts = append(parts, fmt.Sprintf("PR %s conflicts with %s: merge %s and resolve the conflicts.", pr.URL, pr.BaseBranch, pr.BaseBranch))
	}
	return strings.Join(parts, "\n\n")
}

func addNote(ctx context.Context, d Deps, t model.Task, text string) {
	if err := d.Tasks.AddNote(ctx, t.Ref, text); err != nil {
		d.Log.Warn(fmt.Sprintf("failed to add note to task %s: %s", t.Ref, err))
	}
}

// sweepSessions marks open interactive sessions whose workmux window is gone as abandoned and returns their tasks
// to todo. The worktree is left for the user. Failures are logged; they never fail the sweep.
func sweepSessions(ctx context.Context, d Deps) []session.Session {
	var open []session.Session
	for _, s := range session.List(d.Cwd) {
		if s.Status == session.Open {
			open = append(open, s)
		}
	}
	if len(open) == 0 || d.Workmux == nil {
		return nil
	}
	d.Log.Section("Interactive sessions")
	worktrees, err := d.Workmux.List(ctx, d.Cwd)
	if err != nil {
		d.Log.Warn("skipped checking interactive sessions: " + err.Error())
		return nil
	}
	windowOpen := map[string]bool{}
	for _, w := range worktrees {
		windowOpen[w.Handle] = w.IsOpen
	}
	var idx *tasks.Index
	var abandoned []session.Session
	for _, s := range open {
		if ctx.Err() != nil {
			d.Log.Warn("stopped before checking the remaining sessions")
			break
		}
		if windowOpen[s.Handle] {
			d.Log.Step(ansi.Bold(s.Handle) + " " + s.Agent + " session open")
			continue
		}
		ctx := context.WithoutCancel(ctx)
		if s.Task != nil && idx == nil {
			loaded, err := d.Tasks.GetIndex(ctx)
			if err != nil {
				d.Log.Warn("skipped abandoning sessions: " + err.Error())
				return abandoned
			}
			idx = &loaded
		}
		if err := abandon(ctx, d, s, idx); err != nil {
			d.Log.Fail(ansi.Bold(s.Handle) + " " + s.Agent + " session")
			d.Log.Field("error", err.Error())
			continue
		}
		abandoned = append(abandoned, s)
	}
	return abandoned
}

func abandon(ctx context.Context, d Deps, s session.Session, idx *tasks.Index) error {
	action := "abandoned"
	if t := s.Task; t != nil {
		current, held := heldBy(idx, t.Ref, s.RunID)
		if held {
			if err := d.Tasks.Update(ctx, t.Ref, tasks.Update{Status: model.StatusTodo, RemoveMetadata: tasks.SessionClaimKeys}); err != nil {
				return err
			}
			addNote(ctx, d, current, fmt.Sprintf("%s: abandoned\n\nThe interactive session's window was closed without a submit. Its worktree was kept at %s.", s.Agent, s.WorktreePath))
			action = "abandoned, task " + t.Ref + " moved to todo"
		} else {
			action = "abandoned, task " + t.Ref + " no longer held by it"
		}
	}
	s.Status = session.Abandoned
	if err := session.Save(d.Cwd, s); err != nil {
		return err
	}
	d.Log.Warn(ansi.Bold(s.Handle) + " " + s.Agent + " session window closed")
	d.Log.Field("worktree", s.WorktreePath)
	d.Log.Field("action", action)
	return nil
}

// heldBy finds the task ref and reports whether it is still active under the session's claim.
func heldBy(idx *tasks.Index, ref, runID string) (model.Task, bool) {
	for _, t := range idx.All {
		if t.Ref == ref {
			return t, t.Status == model.StatusActive && t.Metadata[model.MetaClaimedSession] == runID
		}
	}
	return model.Task{}, false
}

func LogSummary(l *log.Logger, r Result) {
	l.Section("Result")
	l.OK(fmt.Sprintf("merged %d · canceled %d · reopened %d · waiting %d", len(r.Merged), len(r.Canceled), len(r.Reopened), len(r.Waiting)))
	if len(r.Abandoned) > 0 {
		l.Warn(fmt.Sprintf("abandoned %d interactive session(s); remove their worktrees with `workmux remove`", len(r.Abandoned)))
	}
	if len(r.Failed) > 0 {
		l.Fail(fmt.Sprintf("%d task(s) failed", len(r.Failed)))
	}
}
