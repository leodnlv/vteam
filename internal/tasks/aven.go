package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"

	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/execx"
	"github.com/leodnlv/vteam/internal/model"
	"golang.org/x/sync/errgroup"
)

var priorityBySeverity = map[model.Severity]string{
	model.SeverityCritical: "urgent",
	model.SeverityHigh:     "high",
	model.SeverityMedium:   "medium",
	model.SeverityLow:      "low",
}

var severityByPriority = map[string]model.Severity{
	"urgent": model.SeverityCritical,
	"high":   model.SeverityHigh,
	"medium": model.SeverityMedium,
	"low":    model.SeverityLow,
	"none":   model.SeverityLow,
}

const showConcurrency = 8

var (
	createdRef   = regexp.MustCompile(`(?m)^created (\S+)`)
	missingScope = regexp.MustCompile(`unknown-(project|label)`)
)

type avenDetail struct {
	Task struct {
		Ref       string           `json:"ref"`
		Title     string           `json:"title"`
		Status    model.TaskStatus `json:"status"`
		Priority  string           `json:"priority"`
		CreatedAt string           `json:"created_at"`
	} `json:"task"`
	Description string            `json:"description"`
	Metadata    map[string]string `json:"metadata"`
	Notes       []struct {
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
	} `json:"notes"`
}

func (d avenDetail) toTask() model.Task {
	severity, ok := severityByPriority[d.Task.Priority]
	if !ok {
		severity = model.SeverityLow
	}
	metadata := d.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	retries, _ := strconv.Atoi(metadata[model.MetaRetryCount])
	notes := make([]model.TaskNote, len(d.Notes))
	for i, n := range d.Notes {
		notes[i] = model.TaskNote{Body: n.Body, CreatedAt: n.CreatedAt}
	}
	return model.Task{
		Ref:         d.Task.Ref,
		Title:       d.Task.Title,
		Status:      d.Task.Status,
		Severity:    severity,
		RetryCount:  retries,
		Metadata:    metadata,
		Description: d.Description,
		Notes:       notes,
		CreatedAt:   d.Task.CreatedAt,
	}
}

// Aven is the Manager backed by the aven CLI. It never touches aven's database directly.
type Aven struct {
	Config config.Tasks
	Exec   execx.Runner
	// GlobalArgs are put before every aven command, e.g. ["--db", path] in tests.
	GlobalArgs []string
}

func NewAven(cfg config.Tasks) *Aven {
	return &Aven{Config: cfg, Exec: execx.OS{}}
}

func (a *Aven) ListReady(ctx context.Context) ([]model.Task, error) {
	return a.list(ctx, "--status", "todo", "--ready")
}

func (a *Aven) ListDone(ctx context.Context) ([]model.Task, error) {
	return a.list(ctx, "--status", "done")
}

func (a *Aven) GetIndex(ctx context.Context) (Index, error) {
	all, err := a.list(ctx)
	if err != nil {
		return Index{}, err
	}
	return NewIndex(all), nil
}

func (a *Aven) Create(ctx context.Context, body model.TaskContentBody, foundBy string, labels []string) (string, error) {
	all := []string{a.Config.Label}
	for _, l := range labels {
		if !slices.Contains(all, l) {
			all = append(all, l)
		}
	}
	if err := a.createLabels(ctx, all); err != nil {
		return "", err
	}
	args := []string{"add", body.Title,
		"--project", a.Config.Project,
		"--status", "inbox",
		"--priority", priorityBySeverity[body.Severity],
	}
	for _, l := range all {
		args = append(args, "--label", l)
	}
	args = append(args, "--metadata", model.MetaFoundBy+"="+foundBy, "--description-stdin")
	out, err := a.aven(ctx, FormatDescription(body), args...)
	if err != nil {
		return "", err
	}
	m := createdRef.FindStringSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("could not parse task ref from aven output: %s", out)
	}
	// aven promotes inbox tasks with priority medium or higher to todo on add; an explicit edit keeps them in inbox.
	if _, err := a.aven(ctx, "", "edit", m[1], "--status", "inbox"); err != nil {
		return "", err
	}
	return m[1], nil
}

func (a *Aven) Update(ctx context.Context, ref string, u Update) error {
	args := []string{"edit", ref}
	if u.Status != "" {
		args = append(args, "--status", string(u.Status))
	}
	keys := make([]string, 0, len(u.Metadata))
	for k, v := range u.Metadata {
		if v != "" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		args = append(args, "--metadata", k+"="+u.Metadata[k])
	}
	for _, k := range u.RemoveMetadata {
		args = append(args, "--remove-metadata", k)
	}
	if err := a.createLabels(ctx, u.AddLabels); err != nil {
		return err
	}
	for _, l := range u.AddLabels {
		args = append(args, "--label", l)
	}
	_, err := a.aven(ctx, "", args...)
	return err
}

func (a *Aven) AddNote(ctx context.Context, ref, text string) error {
	_, err := a.aven(ctx, text, "note", ref, "--stdin")
	return err
}

// createLabels creates each label first, because aven edit --label rejects unknown labels.
func (a *Aven) createLabels(ctx context.Context, labels []string) error {
	for _, l := range labels {
		if _, err := a.aven(ctx, "", "label", "create", l); err != nil {
			return err
		}
	}
	return nil
}

func (a *Aven) list(ctx context.Context, filters ...string) ([]model.Task, error) {
	args := append([]string{"list", "--project", a.Config.Project, "--label", a.Config.Label}, filters...)
	out, err := a.aven(ctx, "", append(args, "--json")...)
	if err != nil {
		var execErr *execx.Error
		if errors.As(err, &execErr) && missingScope.MatchString(execErr.Output()) {
			return []model.Task{}, nil
		}
		return nil, err
	}
	var summaries []struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal([]byte(out), &summaries); err != nil {
		return nil, fmt.Errorf("parse aven list output: %w", err)
	}

	result := make([]model.Task, len(summaries))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(showConcurrency)
	for i, s := range summaries {
		g.Go(func() error {
			out, err := a.aven(gctx, "", "show", s.Ref, "--full", "--json")
			if err != nil {
				return err
			}
			var d avenDetail
			if err := json.Unmarshal([]byte(out), &d); err != nil {
				return fmt.Errorf("parse aven show %s: %w", s.Ref, err)
			}
			result[i] = d.toTask()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return result, nil
}

func (a *Aven) aven(ctx context.Context, stdin string, args ...string) (string, error) {
	full := append([]string(nil), a.GlobalArgs...)
	if a.Config.Workspace != "" {
		full = append(full, "--workspace", a.Config.Workspace)
	}
	full = append(full, args...)
	out, err := a.Exec.Run(ctx, execx.Cmd{Name: "aven", Args: full, Stdin: stdin})
	if errors.Is(err, execx.ErrNotFound) {
		return "", errors.New("aven CLI not found on PATH. vteam requires aven for task management")
	}
	return out, err
}
