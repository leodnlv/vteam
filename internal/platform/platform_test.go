package platform

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/execx"
	"github.com/leodnlv/vteam/internal/model"
)

var ctx = context.Background()

// responder answers each command with the first response whose key prefixes "name arg1 arg2 ...".
func responder(responses map[string]string, failures ...string) *execx.Fake {
	return &execx.Fake{Handler: func(c execx.Cmd) (string, error) {
		line := c.Name + " " + strings.Join(c.Args, " ")
		for _, f := range failures {
			if strings.HasPrefix(line, f) {
				return "", execx.Failure(c, "boom")
			}
		}
		best := ""
		for key := range responses {
			if strings.HasPrefix(line, key) && len(key) > len(best) {
				best = key
			}
		}
		if best == "" {
			return "", nil
		}
		return responses[best], nil
	}}
}

func TestNewPicksThePlatform(t *testing.T) {
	if _, ok := New(model.GitHub, "/r").(*GitHub); !ok {
		t.Error("github")
	}
	if gl, ok := New(model.GitLab, "/r").(*GitLab); !ok || gl.Dir != "/r" {
		t.Error("gitlab")
	}
}

func TestGitHubReviewStatus(t *testing.T) {
	for decision, want := range map[string]model.PRReviewStatus{
		"APPROVED": "approved", "CHANGES_REQUESTED": "changes_requested", "REVIEW_REQUIRED": "review_required",
	} {
		if got := GitHubReviewStatus(decision, []string{"APPROVED"}); got != want {
			t.Errorf("%s -> %s, want %s", decision, got, want)
		}
	}
	cases := []struct {
		states []string
		want   model.PRReviewStatus
	}{
		{[]string{"APPROVED"}, "approved"},
		{[]string{"APPROVED", "CHANGES_REQUESTED"}, "changes_requested"},
		{[]string{"COMMENTED"}, "none"},
		{nil, "none"},
	}
	for _, c := range cases {
		if got := GitHubReviewStatus("", c.states); got != c.want {
			t.Errorf("fallback %v -> %s, want %s", c.states, got, c.want)
		}
	}
}

func TestGitLabReviewStatus(t *testing.T) {
	someone := []any{map[string]any{}}
	cases := []struct {
		name   string
		status string
		a      Approvals
		want   model.PRReviewStatus
	}{
		{"reports requested changes first", "requested_changes", Approvals{true, someone}, "changes_requested"},
		{"approved when someone approved and the rules are met", "mergeable", Approvals{true, someone}, "approved"},
		{"review required when rules are not met", "not_approved", Approvals{false, someone}, "review_required"},
		{"review required without approvals", "not_approved", Approvals{false, nil}, "review_required"},
		{"none when no approvals are needed or given", "mergeable", Approvals{true, nil}, "none"},
	}
	for _, c := range cases {
		if got := GitLabReviewStatus(c.status, c.a); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestGitHubFindReviewablePR(t *testing.T) {
	fake := responder(map[string]string{"gh pr list": `[{"number":7,"title":"T","headRefName":"feat","url":"u7"},{"number":8}]`})
	gh := &GitHub{Exec: fake, Dir: "/repo"}
	pr, err := gh.FindReviewablePR(ctx, []string{"needs-work", "vteam"})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(&model.ReviewablePR{Number: 7, Title: "T", Branch: "feat", URL: "u7"}, pr); diff != "" {
		t.Error(diff)
	}
	c := fake.Calls()[0]
	want := []string{"pr", "list", "--search", "label:needs-work label:vteam", "--json", "number,title,headRefName,url", "--limit", "10"}
	if diff := cmp.Diff(want, c.Args); diff != "" || c.Dir != "/repo" {
		t.Errorf("call %+v\n%s", c, diff)
	}

	fake = responder(map[string]string{"gh pr list": ""})
	pr, err = (&GitHub{Exec: fake}).FindReviewablePR(ctx, nil)
	if err != nil || pr != nil {
		t.Errorf("no PRs: pr %v err %v", pr, err)
	}
	if fake.Calls()[0].Args[3] != "is:open" {
		t.Errorf("search = %q", fake.Calls()[0].Args[3])
	}
}

func TestGitHubReviewComments(t *testing.T) {
	fake := responder(map[string]string{
		"gh pr view 5": `{"reviews":[{"author":{"login":"rev"},"body":"Overall fine","submittedAt":"2026-01-02"},{"author":{"login":"rev"},"body":"  ","submittedAt":"2026-01-01"}],
			"comments":[{"author":{"login":"bob"},"body":"top level","createdAt":"2026-01-04"}]}`,
		"gh repo view": "o/r\n",
		"gh api repos/o/r/pulls/5/comments": `[{"user":{"login":"amy"},"body":"inline","path":"a.ts","line":null,"original_line":9,"created_at":"2026-01-03"},
			{"user":{"login":"amy"},"body":"first","path":"b.ts","line":2,"created_at":"2026-01-01"}]`,
	})
	comments, err := (&GitHub{Exec: fake}).ReviewComments(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.ReviewComment{
		{Author: "amy", Body: "first", Path: "b.ts", Line: 2, CreatedAt: "2026-01-01"},
		{Author: "rev", Body: "Overall fine", CreatedAt: "2026-01-02"},
		{Author: "amy", Body: "inline", Path: "a.ts", Line: 9, CreatedAt: "2026-01-03"},
		{Author: "bob", Body: "top level", CreatedAt: "2026-01-04"},
	}
	if diff := cmp.Diff(want, comments); diff != "" {
		t.Error(diff)
	}
}

func TestGitHubPullRequest(t *testing.T) {
	fake := responder(map[string]string{"gh pr view": `{"number":3,"title":"T","headRefName":"vteam/x","baseRefName":"main","url":"https://github.com/o/r/pull/3",
		"state":"OPEN","labels":[{"name":"vteam"},{"name":"vteam:changes-requested"}],"mergeable":"CONFLICTING","reviewDecision":"","latestReviews":[{"state":"APPROVED"}]}`})
	pr, err := (&GitHub{Exec: fake}).PullRequest(ctx, "https://github.com/o/r/pull/3")
	if err != nil {
		t.Fatal(err)
	}
	want := model.PullRequestInfo{
		ReviewablePR: model.ReviewablePR{Number: 3, Title: "T", Branch: "vteam/x", URL: "https://github.com/o/r/pull/3"},
		State:        "open", BaseBranch: "main", Labels: []string{"vteam", "vteam:changes-requested"},
		Mergeable: "conflicting", Review: "approved",
	}
	if diff := cmp.Diff(want, pr); diff != "" {
		t.Error(diff)
	}
	if args := fake.Calls()[0].Args; args[2] != "https://github.com/o/r/pull/3" || !strings.Contains(args[4], "latestReviews") {
		t.Errorf("args = %v", args)
	}
}

func TestGitHubOpenReviewThreads(t *testing.T) {
	fake := responder(map[string]string{
		"gh repo view": "acme/app\n",
		"gh api graphql": `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[
			{"id":"T_1","isResolved":false,"path":"a.ts","line":null,"originalLine":4,"comments":{"nodes":[{"author":{"login":"amy"},"body":"fix","createdAt":"c1"},{"author":null,"body":"gone","createdAt":"c2"}]}},
			{"id":"T_2","isResolved":true,"path":"b.ts","line":1,"comments":{"nodes":[]}}
		]}}}}}`,
	})
	threads, err := (&GitHub{Exec: fake}).OpenReviewThreads(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.ReviewThread{{ID: "T_1", Path: "a.ts", Line: 4, Comments: []model.ReviewComment{
		{Author: "amy", Body: "fix", CreatedAt: "c1"}, {Author: "ghost", Body: "gone", CreatedAt: "c2"},
	}}}
	if diff := cmp.Diff(want, threads); diff != "" {
		t.Error(diff)
	}
	args := fake.Calls()[1].Args
	if diff := cmp.Diff([]string{"-f", "owner=acme", "-f", "name=app", "-F", "number=12"}, args[4:]); diff != "" {
		t.Errorf("graphql variables:\n%s", diff)
	}
}

func TestGitHubMutations(t *testing.T) {
	fake := responder(nil)
	gh := &GitHub{Exec: fake}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(gh.ReplyToThread(ctx, 1, "T_1", "done"))
	must(gh.ResolveThread(ctx, 1, "T_1"))
	must(gh.PostComment(ctx, 4, "it's `done`"))
	must(gh.RemoveLabel(ctx, 4, "vteam:changes-requested"))

	calls := fake.Calls()
	if a := calls[0].Args; !strings.Contains(a[3], "addPullRequestReviewThreadReply") || a[5] != "id=T_1" || a[7] != "body=done" {
		t.Errorf("reply args = %v", a)
	}
	if a := calls[1].Args; !strings.Contains(a[3], "resolveReviewThread") || a[5] != "id=T_1" {
		t.Errorf("resolve args = %v", a)
	}
	if diff := cmp.Diff([]string{"pr", "comment", "4", "--body", "it's `done`"}, calls[2].Args); diff != "" {
		t.Error(diff)
	}
	if diff := cmp.Diff([]string{"pr", "edit", "4", "--remove-label", "vteam:changes-requested"}, calls[3].Args); diff != "" {
		t.Error(diff)
	}
}

func TestGitHubCreatePR(t *testing.T) {
	fake := responder(map[string]string{"gh pr create": "https://github.com/o/r/pull/9\n"}, "gh label create")
	url, err := (&GitHub{Exec: fake}).CreatePR(ctx, PRRequest{Branch: "vteam/x", BaseBranch: "main", Title: "vteam: coder: x", Body: "body", Labels: []string{"vteam", "bot"}})
	if err != nil || url != "https://github.com/o/r/pull/9" {
		t.Fatalf("url %q err %v", url, err)
	}
	want := [][]string{
		{"gh", "label", "create", "vteam", "--color", "EDEDED"},
		{"gh", "label", "create", "bot", "--color", "EDEDED"},
		{"gh", "pr", "create", "--head", "vteam/x", "--base", "main", "--title", "vteam: coder: x", "--body", "body", "--label", "vteam,bot"},
	}
	if diff := cmp.Diff(want, fake.Args()); diff != "" {
		t.Error(diff)
	}

	fake = responder(nil, "gh pr create")
	if _, err := (&GitHub{Exec: fake}).CreatePR(ctx, PRRequest{}); err == nil {
		t.Error("expected failure")
	}
}

func TestGitLabFindReviewablePR(t *testing.T) {
	fake := responder(map[string]string{"glab api": `[{"iid":4,"title":"T","source_branch":"feat","web_url":"u4"}]`})
	pr, err := (&GitLab{Exec: fake}).FindReviewablePR(ctx, []string{"needs work", "vteam"})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(&model.ReviewablePR{Number: 4, Title: "T", Branch: "feat", URL: "u4"}, pr); diff != "" {
		t.Error(diff)
	}
	if got := fake.Calls()[0].Args[1]; got != "projects/:id/merge_requests?state=opened&labels=needs+work,vteam" {
		t.Errorf("path = %q", got)
	}
	fake = responder(map[string]string{"glab api": "[]"})
	if pr, _ := (&GitLab{Exec: fake}).FindReviewablePR(ctx, nil); pr != nil || fake.Calls()[0].Args[1] != "projects/:id/merge_requests?state=opened" {
		t.Errorf("pr %v path %q", pr, fake.Calls()[0].Args[1])
	}
}

const glDiscussions = `[
	{"id":"D1","notes":[{"author":{"username":"amy"},"body":"rename","system":false,"resolvable":true,"resolved":false,"position":{"new_path":"a.ts","new_line":3},"created_at":"2026-01-02"},
		{"author":{"username":"bot"},"body":"changed line","system":true,"resolvable":false,"resolved":false,"created_at":"2026-01-03"}]},
	{"id":"D2","notes":[{"author":{"username":"bob"},"body":"done already","system":false,"resolvable":true,"resolved":true,"created_at":"2026-01-01"}]},
	{"id":"D3","notes":[{"author":{"username":"cat"},"body":"general","system":false,"resolvable":false,"resolved":false,"created_at":"2026-01-04"}]}
]`

func TestGitLabReviewComments(t *testing.T) {
	fake := responder(map[string]string{"glab api": glDiscussions})
	comments, err := (&GitLab{Exec: fake}).ReviewComments(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.ReviewComment{
		{Author: "bob", Body: "done already", CreatedAt: "2026-01-01"},
		{Author: "amy", Body: "rename", Path: "a.ts", Line: 3, CreatedAt: "2026-01-02"},
		{Author: "cat", Body: "general", CreatedAt: "2026-01-04"},
	}
	if diff := cmp.Diff(want, comments); diff != "" {
		t.Error(diff)
	}
	if got := fake.Calls()[0].Args[1]; got != "projects/:id/merge_requests/6/discussions" {
		t.Errorf("path = %q", got)
	}
}

func TestGitLabOpenReviewThreads(t *testing.T) {
	fake := responder(map[string]string{"glab api": glDiscussions})
	threads, err := (&GitLab{Exec: fake}).OpenReviewThreads(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.ReviewThread{{ID: "D1", Path: "a.ts", Line: 3, Comments: []model.ReviewComment{{Author: "amy", Body: "rename", CreatedAt: "2026-01-02"}}}}
	if diff := cmp.Diff(want, threads); diff != "" {
		t.Error(diff)
	}
	if got := fake.Calls()[0].Args[1]; got != "projects/:id/merge_requests/6/discussions?per_page=100" {
		t.Errorf("path = %q", got)
	}
}

func TestGitLabPullRequest(t *testing.T) {
	mr := func(state, mergeStatus string, conflicts bool) string {
		return `{"iid":11,"title":"T","source_branch":"vteam/x","target_branch":"develop","web_url":"https://gitlab.com/o/r/-/merge_requests/11",
			"state":"` + state + `","labels":["vteam"],"has_conflicts":` + map[bool]string{true: "true", false: "false"}[conflicts] + `,
			"merge_status":"` + mergeStatus + `","detailed_merge_status":"mergeable"}`
	}
	cases := []struct {
		name, state, mergeStatus string
		conflicts                bool
		wantState                model.PRState
		wantMergeable            model.PRMergeability
	}{
		{"open and mergeable", "opened", "can_be_merged", false, "open", "mergeable"},
		{"conflicting", "opened", "cannot_be_merged", true, "open", "conflicting"},
		{"still checking", "opened", "checking", false, "open", "unknown"},
		{"merged", "merged", "can_be_merged", false, "merged", "mergeable"},
		{"locked counts as closed", "locked", "can_be_merged", false, "closed", "mergeable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := responder(map[string]string{
				"glab api projects/:id/merge_requests/11":           mr(c.state, c.mergeStatus, c.conflicts),
				"glab api projects/:id/merge_requests/11/approvals": `{"approved":true,"approved_by":[{"user":{}}]}`,
			})
			pr, err := (&GitLab{Exec: fake}).PullRequest(ctx, "https://gitlab.com/o/r/-/merge_requests/11")
			if err != nil {
				t.Fatal(err)
			}
			want := model.PullRequestInfo{
				ReviewablePR: model.ReviewablePR{Number: 11, Title: "T", Branch: "vteam/x", URL: "https://gitlab.com/o/r/-/merge_requests/11"},
				State:        c.wantState, BaseBranch: "develop", Labels: []string{"vteam"}, Mergeable: c.wantMergeable, Review: "approved",
			}
			if diff := cmp.Diff(want, pr); diff != "" {
				t.Error(diff)
			}
		})
	}

	t.Run("rejects URLs that are not merge requests", func(t *testing.T) {
		if _, err := (&GitLab{Exec: responder(nil)}).PullRequest(ctx, "https://github.com/o/r/pull/1"); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestGitLabMutations(t *testing.T) {
	fake := responder(nil)
	gl := &GitLab{Exec: fake}
	for _, err := range []error{
		gl.ReplyToThread(ctx, 6, "D1", "renamed"),
		gl.ResolveThread(ctx, 6, "D1"),
		gl.PostComment(ctx, 6, "note"),
		gl.RemoveLabel(ctx, 6, "vteam:changes-requested"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := [][]string{
		{"glab", "api", "projects/:id/merge_requests/6/discussions/D1/notes", "-f", "body=renamed"},
		{"glab", "api", "-X", "PUT", "projects/:id/merge_requests/6/discussions/D1", "-f", "resolved=true"},
		{"glab", "mr", "note", "6", "-m", "note"},
		{"glab", "mr", "update", "6", "--unlabel", "vteam:changes-requested"},
	}
	if diff := cmp.Diff(want, fake.Args()); diff != "" {
		t.Error(diff)
	}
}

func TestGitLabCreatePR(t *testing.T) {
	fake := responder(map[string]string{"glab mr create": "Creating merge request...\n!12 vteam: x (vteam/x)\n https://gitlab.com/o/r/-/merge_requests/12\n"}, "glab label create")
	url, err := (&GitLab{Exec: fake}).CreatePR(ctx, PRRequest{Branch: "vteam/x", BaseBranch: "main", Title: "t", Body: "b", Labels: []string{"vteam"}})
	if err != nil || url != "https://gitlab.com/o/r/-/merge_requests/12" {
		t.Fatalf("url %q err %v", url, err)
	}
	want := [][]string{
		{"glab", "label", "create", "vteam", "--color", "#EDEDED"},
		{"glab", "mr", "create", "--source-branch", "vteam/x", "--target-branch", "main", "--title", "t", "--description", "b", "--no-editor", "--label", "vteam"},
	}
	if diff := cmp.Diff(want, fake.Args()); diff != "" {
		t.Error(diff)
	}

	fake = responder(map[string]string{"glab mr create": "  no url here  \n"})
	if url, _ := (&GitLab{Exec: fake}).CreatePR(ctx, PRRequest{}); url != "no url here" {
		t.Errorf("url = %q", url)
	}
}

func TestGitLabRepoSlug(t *testing.T) {
	fake := responder(map[string]string{"glab api projects/:id": `{"path_with_namespace":"group/app"}`})
	if slug, err := (&GitLab{Exec: fake}).RepoSlug(ctx); err != nil || slug != "group/app" {
		t.Errorf("slug %q err %v", slug, err)
	}
}

func TestErrorsPropagate(t *testing.T) {
	gh := &GitHub{Exec: responder(nil, "gh")}
	gl := &GitLab{Exec: responder(nil, "glab")}
	var execErr *execx.Error
	for name, err := range map[string]error{
		"gh find":     second(gh.FindReviewablePR(ctx, nil)),
		"gh comments": second(gh.ReviewComments(ctx, 1)),
		"gh pr":       second(gh.PullRequest(ctx, "u")),
		"gh threads":  second(gh.OpenReviewThreads(ctx, 1)),
		"gl find":     second(gl.FindReviewablePR(ctx, nil)),
		"gl comments": second(gl.ReviewComments(ctx, 1)),
		"gl pr":       second(gl.PullRequest(ctx, "/merge_requests/1")),
		"gl threads":  second(gl.OpenReviewThreads(ctx, 1)),
		"gl slug":     second(gl.RepoSlug(ctx)),
		"gl create":   second(gl.CreatePR(ctx, PRRequest{})),
	} {
		if !errors.As(err, &execErr) {
			t.Errorf("%s: err = %v", name, err)
		}
	}

	bad := &GitHub{Exec: responder(map[string]string{"gh pr view": "not json"})}
	if _, err := bad.PullRequest(ctx, "u"); err == nil || !strings.Contains(err.Error(), "parse gh pr view") {
		t.Errorf("bad JSON err = %v", err)
	}
}

func second[T any](_ T, err error) error { return err }
