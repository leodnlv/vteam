package platform

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/leodnlv/vteam/internal/execx"
	"github.com/leodnlv/vteam/internal/model"
)

type GitLab struct {
	Exec execx.Runner
	Dir  string
}

func (g *GitLab) glab(ctx context.Context, args ...string) (string, error) {
	return run(ctx, g.Exec, g.Dir, "glab", args...)
}

func (g *GitLab) mrPath(number int, rest string) string {
	return fmt.Sprintf("projects/:id/merge_requests/%d%s", number, rest)
}

func (g *GitLab) FindReviewablePR(ctx context.Context, labels []string) (*model.ReviewablePR, error) {
	path := "projects/:id/merge_requests?state=opened"
	if len(labels) > 0 {
		escaped := make([]string, len(labels))
		for i, l := range labels {
			escaped[i] = url.QueryEscape(l)
		}
		path += "&labels=" + strings.Join(escaped, ",")
	}
	out, err := g.glab(ctx, "api", path)
	if err != nil {
		return nil, err
	}
	var mrs []struct {
		IID          int    `json:"iid"`
		Title        string `json:"title"`
		SourceBranch string `json:"source_branch"`
		WebURL       string `json:"web_url"`
	}
	if err := decode(out, "glab merge requests", &mrs); err != nil || len(mrs) == 0 {
		return nil, err
	}
	mr := mrs[0]
	return &model.ReviewablePR{Number: mr.IID, Title: mr.Title, Branch: mr.SourceBranch, URL: mr.WebURL}, nil
}

func (g *GitLab) RepoSlug(ctx context.Context) (string, error) {
	out, err := g.glab(ctx, "api", "projects/:id")
	if err != nil {
		return "", err
	}
	var project struct {
		PathWithNamespace string `json:"path_with_namespace"`
	}
	err = decode(out, "glab project", &project)
	return project.PathWithNamespace, err
}

type glNote struct {
	Author struct {
		Username string `json:"username"`
	} `json:"author"`
	Body       string `json:"body"`
	System     bool   `json:"system"`
	Resolvable bool   `json:"resolvable"`
	Resolved   bool   `json:"resolved"`
	Position   *struct {
		NewPath string `json:"new_path"`
		NewLine *int   `json:"new_line"`
	} `json:"position"`
	CreatedAt string `json:"created_at"`
}

type glDiscussion struct {
	ID    string   `json:"id"`
	Notes []glNote `json:"notes"`
}

func (g *GitLab) discussions(ctx context.Context, number int, query string) ([]glDiscussion, error) {
	out, err := g.glab(ctx, "api", g.mrPath(number, "/discussions"+query))
	if err != nil {
		return nil, err
	}
	var discussions []glDiscussion
	err = decode(out, "glab discussions", &discussions)
	return discussions, err
}

func (g *GitLab) ReviewComments(ctx context.Context, number int) ([]model.ReviewComment, error) {
	discussions, err := g.discussions(ctx, number, "")
	if err != nil {
		return nil, err
	}
	var comments []model.ReviewComment
	for _, d := range discussions {
		for _, n := range d.Notes {
			if n.System {
				continue
			}
			c := model.ReviewComment{Author: n.Author.Username, Body: n.Body, CreatedAt: n.CreatedAt}
			if n.Position != nil {
				c.Path, c.Line = n.Position.NewPath, firstLine(n.Position.NewLine)
			}
			comments = append(comments, c)
		}
	}
	return sortByCreated(comments), nil
}

var mrNumber = regexp.MustCompile(`/merge_requests/(\d+)`)

func (g *GitLab) PullRequest(ctx context.Context, mrURL string) (model.PullRequestInfo, error) {
	m := mrNumber.FindStringSubmatch(mrURL)
	if m == nil {
		return model.PullRequestInfo{}, fmt.Errorf("not a GitLab merge request URL: %s", mrURL)
	}
	number, _ := strconv.Atoi(m[1])
	out, err := g.glab(ctx, "api", g.mrPath(number, ""))
	if err != nil {
		return model.PullRequestInfo{}, err
	}
	var mr struct {
		IID                 int      `json:"iid"`
		Title               string   `json:"title"`
		SourceBranch        string   `json:"source_branch"`
		TargetBranch        string   `json:"target_branch"`
		WebURL              string   `json:"web_url"`
		State               string   `json:"state"`
		Labels              []string `json:"labels"`
		HasConflicts        bool     `json:"has_conflicts"`
		MergeStatus         string   `json:"merge_status"`
		DetailedMergeStatus string   `json:"detailed_merge_status"`
	}
	if err := decode(out, "glab merge request", &mr); err != nil {
		return model.PullRequestInfo{}, err
	}
	out, err = g.glab(ctx, "api", g.mrPath(number, "/approvals"))
	if err != nil {
		return model.PullRequestInfo{}, err
	}
	var approvals Approvals
	if err := decode(out, "glab approvals", &approvals); err != nil {
		return model.PullRequestInfo{}, err
	}
	state := model.PRClosed
	switch mr.State {
	case "opened":
		state = model.PROpen
	case "merged":
		state = model.PRMerged
	}
	labels := mr.Labels
	if labels == nil {
		labels = []string{}
	}
	return model.PullRequestInfo{
		ReviewablePR: model.ReviewablePR{Number: mr.IID, Title: mr.Title, Branch: mr.SourceBranch, URL: mr.WebURL},
		State:        state,
		BaseBranch:   mr.TargetBranch,
		Labels:       labels,
		Mergeable:    gitLabMergeability(mr.HasConflicts, mr.MergeStatus),
		Review:       GitLabReviewStatus(mr.DetailedMergeStatus, approvals),
	}, nil
}

func gitLabMergeability(hasConflicts bool, mergeStatus string) model.PRMergeability {
	if hasConflicts {
		return model.Conflicting
	}
	if slices.Contains([]string{"unchecked", "checking", "cannot_be_merged_recheck"}, mergeStatus) {
		return model.MergeabilityUnknown
	}
	return model.Mergeable
}

type Approvals struct {
	Approved   bool  `json:"approved"`
	ApprovedBy []any `json:"approved_by"`
}

func GitLabReviewStatus(detailedMergeStatus string, a Approvals) model.PRReviewStatus {
	switch {
	case detailedMergeStatus == "requested_changes":
		return model.ReviewChangesRequested
	case len(a.ApprovedBy) > 0 && a.Approved:
		return model.ReviewApproved
	case a.Approved:
		return model.ReviewNone
	}
	return model.ReviewRequired
}

func (g *GitLab) OpenReviewThreads(ctx context.Context, number int) ([]model.ReviewThread, error) {
	discussions, err := g.discussions(ctx, number, "?per_page=100")
	if err != nil {
		return nil, err
	}
	threads := []model.ReviewThread{}
	for _, d := range discussions {
		open := slices.ContainsFunc(d.Notes, func(n glNote) bool { return n.Resolvable && !n.Resolved })
		if !open {
			continue
		}
		thread := model.ReviewThread{ID: d.ID}
		if p := d.Notes[0].Position; p != nil {
			thread.Path, thread.Line = p.NewPath, firstLine(p.NewLine)
		}
		for _, n := range d.Notes {
			if !n.System {
				thread.Comments = append(thread.Comments, model.ReviewComment{Author: n.Author.Username, Body: n.Body, CreatedAt: n.CreatedAt})
			}
		}
		threads = append(threads, thread)
	}
	return threads, nil
}

func (g *GitLab) ReplyToThread(ctx context.Context, number int, threadID, body string) error {
	_, err := g.glab(ctx, "api", g.mrPath(number, "/discussions/"+threadID+"/notes"), "-f", "body="+body)
	return err
}

func (g *GitLab) ResolveThread(ctx context.Context, number int, threadID string) error {
	_, err := g.glab(ctx, "api", "-X", "PUT", g.mrPath(number, "/discussions/"+threadID), "-f", "resolved=true")
	return err
}

func (g *GitLab) PostComment(ctx context.Context, number int, body string) error {
	_, err := g.glab(ctx, "mr", "note", strconv.Itoa(number), "-m", body)
	return err
}

func (g *GitLab) RemoveLabel(ctx context.Context, number int, label string) error {
	_, err := g.glab(ctx, "mr", "update", strconv.Itoa(number), "--unlabel", label)
	return err
}

var urlPattern = regexp.MustCompile(`https?://\S+`)

func (g *GitLab) CreatePR(ctx context.Context, req PRRequest) (string, error) {
	args := []string{"mr", "create", "--source-branch", req.Branch, "--target-branch", req.BaseBranch,
		"--title", req.Title, "--description", req.Body, "--no-editor"}
	for _, l := range req.Labels {
		// Fails when the label exists, which is fine.
		_, _ = g.glab(ctx, "label", "create", l, "--color", "#EDEDED")
		args = append(args, "--label", l)
	}
	out, err := g.glab(ctx, args...)
	if err != nil {
		return "", err
	}
	if u := urlPattern.FindString(out); u != "" {
		return u, nil
	}
	return strings.TrimSpace(out), nil
}
