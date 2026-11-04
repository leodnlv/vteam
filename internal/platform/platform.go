// Package platform talks to GitHub (gh) and GitLab (glab) about pull requests.
package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/leodnlv/vteam/internal/execx"
	"github.com/leodnlv/vteam/internal/model"
)

type PRRequest struct {
	Branch     string
	BaseBranch string
	Title      string
	Body       string
	Labels     []string
}

type Platform interface {
	// FindReviewablePR returns the first open PR with all labels, or nil when there is none.
	FindReviewablePR(ctx context.Context, labels []string) (*model.ReviewablePR, error)
	ReviewComments(ctx context.Context, number int) ([]model.ReviewComment, error)
	RepoSlug(ctx context.Context) (string, error)
	PullRequest(ctx context.Context, url string) (model.PullRequestInfo, error)
	// OpenReviewThreads returns the unresolved review threads; resolved threads and top-level comments are left out.
	OpenReviewThreads(ctx context.Context, number int) ([]model.ReviewThread, error)
	ReplyToThread(ctx context.Context, number int, threadID, body string) error
	ResolveThread(ctx context.Context, number int, threadID string) error
	PostComment(ctx context.Context, number int, body string) error
	RemoveLabel(ctx context.Context, number int, label string) error
	CreatePR(ctx context.Context, req PRRequest) (string, error)
}

func New(p model.Platform, dir string) Platform {
	if p == model.GitLab {
		return &GitLab{Exec: execx.OS{}, Dir: dir}
	}
	return &GitHub{Exec: execx.OS{}, Dir: dir}
}

func run(ctx context.Context, r execx.Runner, dir, name string, args ...string) (string, error) {
	return r.Run(ctx, execx.Cmd{Name: name, Args: args, Dir: dir})
}

func decode(out, what string, v any) error {
	out = strings.TrimSpace(out)
	if out == "" {
		out = "null"
	}
	if err := json.Unmarshal([]byte(out), v); err != nil {
		return fmt.Errorf("parse %s: %w", what, err)
	}
	return nil
}

func sortByCreated(comments []model.ReviewComment) []model.ReviewComment {
	slices.SortStableFunc(comments, func(a, b model.ReviewComment) int { return strings.Compare(a.CreatedAt, b.CreatedAt) })
	return comments
}
