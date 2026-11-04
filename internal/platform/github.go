package platform

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/leodnlv/vteam/internal/execx"
	"github.com/leodnlv/vteam/internal/model"
)

type GitHub struct {
	Exec execx.Runner
	Dir  string
}

func (g *GitHub) gh(ctx context.Context, args ...string) (string, error) {
	return run(ctx, g.Exec, g.Dir, "gh", args...)
}

func (g *GitHub) FindReviewablePR(ctx context.Context, labels []string) (*model.ReviewablePR, error) {
	search := "is:open"
	if len(labels) > 0 {
		parts := make([]string, len(labels))
		for i, l := range labels {
			parts[i] = "label:" + l
		}
		search = strings.Join(parts, " ")
	}
	out, err := g.gh(ctx, "pr", "list", "--search", search, "--json", "number,title,headRefName,url", "--limit", "10")
	if err != nil {
		return nil, err
	}
	var items []struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		HeadRefName string `json:"headRefName"`
		URL         string `json:"url"`
	}
	if err := decode(out, "gh pr list", &items); err != nil || len(items) == 0 {
		return nil, err
	}
	pr := items[0]
	return &model.ReviewablePR{Number: pr.Number, Title: pr.Title, Branch: pr.HeadRefName, URL: pr.URL}, nil
}

func (g *GitHub) RepoSlug(ctx context.Context) (string, error) {
	out, err := g.gh(ctx, "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	return strings.TrimSpace(out), err
}

type ghAuthor struct {
	Login string `json:"login"`
}

func (g *GitHub) ReviewComments(ctx context.Context, number int) ([]model.ReviewComment, error) {
	out, err := g.gh(ctx, "pr", "view", strconv.Itoa(number), "--json", "reviews,comments")
	if err != nil {
		return nil, err
	}
	var pr struct {
		Reviews []struct {
			Author      ghAuthor `json:"author"`
			Body        string   `json:"body"`
			SubmittedAt string   `json:"submittedAt"`
		} `json:"reviews"`
		Comments []struct {
			Author    ghAuthor `json:"author"`
			Body      string   `json:"body"`
			CreatedAt string   `json:"createdAt"`
		} `json:"comments"`
	}
	if err := decode(out, "gh pr view", &pr); err != nil {
		return nil, err
	}
	slug, err := g.RepoSlug(ctx)
	if err != nil {
		return nil, err
	}
	out, err = g.gh(ctx, "api", fmt.Sprintf("repos/%s/pulls/%d/comments", slug, number))
	if err != nil {
		return nil, err
	}
	var inline []struct {
		User         ghAuthor `json:"user"`
		Body         string   `json:"body"`
		Path         string   `json:"path"`
		Line         *int     `json:"line"`
		OriginalLine *int     `json:"original_line"`
		CreatedAt    string   `json:"created_at"`
	}
	if err := decode(out, "gh api pull comments", &inline); err != nil {
		return nil, err
	}

	var comments []model.ReviewComment
	for _, r := range pr.Reviews {
		if strings.TrimSpace(r.Body) != "" {
			comments = append(comments, model.ReviewComment{Author: r.Author.Login, Body: r.Body, CreatedAt: r.SubmittedAt})
		}
	}
	for _, c := range inline {
		comments = append(comments, model.ReviewComment{Author: c.User.Login, Body: c.Body, Path: c.Path,
			Line: firstLine(c.Line, c.OriginalLine), CreatedAt: c.CreatedAt})
	}
	for _, c := range pr.Comments {
		comments = append(comments, model.ReviewComment{Author: c.Author.Login, Body: c.Body, CreatedAt: c.CreatedAt})
	}
	return sortByCreated(comments), nil
}

func firstLine(lines ...*int) int {
	for _, l := range lines {
		if l != nil {
			return *l
		}
	}
	return 0
}

func (g *GitHub) PullRequest(ctx context.Context, url string) (model.PullRequestInfo, error) {
	out, err := g.gh(ctx, "pr", "view", url, "--json",
		"number,title,headRefName,baseRefName,url,state,labels,mergeable,reviewDecision,latestReviews")
	if err != nil {
		return model.PullRequestInfo{}, err
	}
	var pr struct {
		Number         int                      `json:"number"`
		Title          string                   `json:"title"`
		HeadRefName    string                   `json:"headRefName"`
		BaseRefName    string                   `json:"baseRefName"`
		URL            string                   `json:"url"`
		State          string                   `json:"state"`
		Labels         []struct{ Name string }  `json:"labels"`
		Mergeable      string                   `json:"mergeable"`
		ReviewDecision string                   `json:"reviewDecision"`
		LatestReviews  []struct{ State string } `json:"latestReviews"`
	}
	if err := decode(out, "gh pr view", &pr); err != nil {
		return model.PullRequestInfo{}, err
	}
	labels := make([]string, len(pr.Labels))
	for i, l := range pr.Labels {
		labels[i] = l.Name
	}
	states := make([]string, len(pr.LatestReviews))
	for i, r := range pr.LatestReviews {
		states[i] = r.State
	}
	return model.PullRequestInfo{
		ReviewablePR: model.ReviewablePR{Number: pr.Number, Title: pr.Title, Branch: pr.HeadRefName, URL: pr.URL},
		State:        model.PRState(strings.ToLower(pr.State)),
		BaseBranch:   pr.BaseRefName,
		Labels:       labels,
		Mergeable:    model.PRMergeability(strings.ToLower(pr.Mergeable)),
		Review:       GitHubReviewStatus(pr.ReviewDecision, states),
	}, nil
}

// GitHubReviewStatus uses reviewDecision when the base branch requires reviews, and otherwise
// falls back to each reviewer's latest review, since reviewDecision is then empty.
func GitHubReviewStatus(decision string, latestReviewStates []string) model.PRReviewStatus {
	if decision != "" {
		return model.PRReviewStatus(strings.ToLower(decision))
	}
	switch {
	case slices.Contains(latestReviewStates, "CHANGES_REQUESTED"):
		return model.ReviewChangesRequested
	case slices.Contains(latestReviewStates, "APPROVED"):
		return model.ReviewApproved
	}
	return model.ReviewNone
}

const reviewThreadsQuery = `
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100) {
        nodes {
          id
          isResolved
          path
          line
          originalLine
          comments(first: 100) {
            nodes { author { login } body createdAt }
          }
        }
      }
    }
  }
}`

type gqlVar struct {
	key   string
	value string
	raw   bool
}

func (g *GitHub) graphQL(ctx context.Context, query string, vars ...gqlVar) (string, error) {
	args := []string{"api", "graphql", "-f", "query=" + query}
	for _, v := range vars {
		flag := "-f"
		if v.raw {
			flag = "-F"
		}
		args = append(args, flag, v.key+"="+v.value)
	}
	return g.gh(ctx, args...)
}

func (g *GitHub) OpenReviewThreads(ctx context.Context, number int) ([]model.ReviewThread, error) {
	slug, err := g.RepoSlug(ctx)
	if err != nil {
		return nil, err
	}
	owner, name, _ := strings.Cut(slug, "/")
	out, err := g.graphQL(ctx, reviewThreadsQuery,
		gqlVar{key: "owner", value: owner}, gqlVar{key: "name", value: name}, gqlVar{key: "number", value: strconv.Itoa(number), raw: true})
	if err != nil {
		return nil, err
	}
	var data struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							ID           string `json:"id"`
							IsResolved   bool   `json:"isResolved"`
							Path         string `json:"path"`
							Line         *int   `json:"line"`
							OriginalLine *int   `json:"originalLine"`
							Comments     struct {
								Nodes []struct {
									Author    *ghAuthor `json:"author"`
									Body      string    `json:"body"`
									CreatedAt string    `json:"createdAt"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := decode(out, "review threads", &data); err != nil {
		return nil, err
	}
	threads := []model.ReviewThread{}
	for _, n := range data.Data.Repository.PullRequest.ReviewThreads.Nodes {
		if n.IsResolved {
			continue
		}
		thread := model.ReviewThread{ID: n.ID, Path: n.Path, Line: firstLine(n.Line, n.OriginalLine)}
		for _, c := range n.Comments.Nodes {
			author := "ghost"
			if c.Author != nil {
				author = c.Author.Login
			}
			thread.Comments = append(thread.Comments, model.ReviewComment{Author: author, Body: c.Body, CreatedAt: c.CreatedAt})
		}
		threads = append(threads, thread)
	}
	return threads, nil
}

func (g *GitHub) ReplyToThread(ctx context.Context, _ int, threadID, body string) error {
	_, err := g.graphQL(ctx, `mutation($id: ID!, $body: String!) {
  addPullRequestReviewThreadReply(input: { pullRequestReviewThreadId: $id, body: $body }) { comment { id } }
}`, gqlVar{key: "id", value: threadID}, gqlVar{key: "body", value: body})
	return err
}

func (g *GitHub) ResolveThread(ctx context.Context, _ int, threadID string) error {
	_, err := g.graphQL(ctx, `mutation($id: ID!) {
  resolveReviewThread(input: { threadId: $id }) { thread { isResolved } }
}`, gqlVar{key: "id", value: threadID})
	return err
}

func (g *GitHub) PostComment(ctx context.Context, number int, body string) error {
	_, err := g.gh(ctx, "pr", "comment", strconv.Itoa(number), "--body", body)
	return err
}

func (g *GitHub) RemoveLabel(ctx context.Context, number int, label string) error {
	_, err := g.gh(ctx, "pr", "edit", strconv.Itoa(number), "--remove-label", label)
	return err
}

func (g *GitHub) CreatePR(ctx context.Context, req PRRequest) (string, error) {
	args := []string{"pr", "create", "--head", req.Branch, "--base", req.BaseBranch, "--title", req.Title, "--body", req.Body}
	if len(req.Labels) > 0 {
		for _, l := range req.Labels {
			// Fails when the label exists, which is fine.
			_, _ = g.gh(ctx, "label", "create", l, "--color", "EDEDED")
		}
		args = append(args, "--label", strings.Join(req.Labels, ","))
	}
	out, err := g.gh(ctx, args...)
	return strings.TrimSpace(out), err
}
