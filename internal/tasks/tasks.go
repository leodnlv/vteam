// Package tasks defines the task manager vteam uses to read and transition tasks, and how tasks are routed to agents.
package tasks

import (
	"context"
	"slices"
	"strings"

	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/model"
)

type Index struct {
	All      []model.Task
	ByStatus map[model.TaskStatus][]model.Task
}

func NewIndex(all []model.Task) Index {
	idx := Index{All: all, ByStatus: map[model.TaskStatus][]model.Task{}}
	for _, t := range all {
		idx.ByStatus[t.Status] = append(idx.ByStatus[t.Status], t)
	}
	return idx
}

type Update struct {
	Status model.TaskStatus
	// Metadata entries with an empty value are skipped.
	Metadata       map[string]string
	RemoveMetadata []string
	AddLabels      []string
}

type Manager interface {
	ListReady(ctx context.Context) ([]model.Task, error)
	ListDone(ctx context.Context) ([]model.Task, error)
	GetIndex(ctx context.Context) (Index, error)
	Create(ctx context.Context, body model.TaskContentBody, foundBy string, labels []string) (string, error)
	Update(ctx context.Context, ref string, u Update) error
	AddNote(ctx context.Context, ref, text string) error
}

func MatchesFilter(task model.Task, filter config.TaskFilter) bool {
	for key, accepted := range filter {
		if !slices.Contains(accepted, task.Metadata[key]) {
			return false
		}
	}
	return true
}

// PickNext returns the ready task an agent should work on next: filtered, under the retry limit,
// highest severity first, then oldest.
func PickNext(ready []model.Task, filter config.TaskFilter, maxRetries int) (model.Task, bool) {
	var candidates []model.Task
	for _, t := range ready {
		if MatchesFilter(t, filter) && t.RetryCount < maxRetries {
			candidates = append(candidates, t)
		}
	}
	if len(candidates) == 0 {
		return model.Task{}, false
	}
	slices.SortStableFunc(candidates, func(a, b model.Task) int {
		if d := a.Severity.Priority() - b.Severity.Priority(); d != 0 {
			return d
		}
		return strings.Compare(a.CreatedAt, b.CreatedAt)
	})
	return candidates[0], true
}

func FormatDescription(body model.TaskContentBody) string {
	sections := []string{"## Description\n\n" + body.Description}
	if body.SuggestedFix != "" {
		sections = append(sections, "## Suggested Fix\n\n"+body.SuggestedFix)
	}
	files := make([]string, len(body.Files))
	for i, f := range body.Files {
		files[i] = "- `" + f + "`"
	}
	sections = append(sections, "## Affected Files\n\n"+strings.Join(files, "\n"))
	return strings.Join(sections, "\n\n")
}
