// Package ensuremerge moves done tasks through review: it records merges, cancels tasks whose PR was closed,
// and reopens tasks whose PR has the changes-requested label or conflicts with its base branch.
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
	"github.com/leodnlv/vteam/internal/tasks"
)

const Name = "merge-ensurer"

type Deps struct {
	Config   *config.Config
	Log      *log.Logger
	Tasks    tasks.Manager
	Platform platform.Platform
}

type Result struct {
	Merged, Canceled, Reopened, Waiting, Failed []model.Task
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
		addNote(ctx, d, t, fmt.Sprintf("%s: canceled\n\nPR %s was closed without merging.", Name, pr.URL))
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
	parts := []string{Name + ": reopened"}
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

func LogSummary(l *log.Logger, r Result) {
	l.Section("Result")
	l.OK(fmt.Sprintf("merged %d · canceled %d · reopened %d · waiting %d", len(r.Merged), len(r.Canceled), len(r.Reopened), len(r.Waiting)))
	if len(r.Failed) > 0 {
		l.Fail(fmt.Sprintf("%d task(s) failed", len(r.Failed)))
	}
}
