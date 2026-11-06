package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/leodnlv/vteam/internal/claude"
	"github.com/leodnlv/vteam/internal/git"
	"github.com/leodnlv/vteam/internal/git/gittest"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/platform"
	"github.com/leodnlv/vteam/internal/workmux"
)

type fakeClaude struct {
	mu        sync.Mutex
	calls     []claude.Options
	responses []func(claude.Options) claude.Result
	fallback  func(claude.Options) claude.Result
}

func result(text string, exitCode int) claude.Result {
	cost := 0.01
	return claude.Result{ExitCode: exitCode, ResultText: text, CostUSD: &cost}
}

func (f *fakeClaude) then(fn func(claude.Options) claude.Result) *fakeClaude {
	f.responses = append(f.responses, fn)
	return f
}

func (f *fakeClaude) thenText(text string) *fakeClaude {
	return f.then(func(claude.Options) claude.Result { return result(text, 0) })
}

// Run calls the next response; a response may cancel the run to stop it, and then Run fails like a killed claude.
func (f *fakeClaude) Run(ctx context.Context, opts claude.Options) (claude.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, opts)
	var fn func(claude.Options) claude.Result
	if len(f.responses) > 0 {
		fn, f.responses = f.responses[0], f.responses[1:]
	} else {
		fn = f.fallback
	}
	f.mu.Unlock()
	if fn == nil {
		return claude.Result{}, errors.New("unexpected claude call")
	}
	res := fn(opts)
	if err := ctx.Err(); err != nil {
		return claude.Result{ExitCode: -1}, err
	}
	return res, nil
}

func (f *fakeClaude) Calls() []claude.Options {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]claude.Options(nil), f.calls...)
}

type pushCall struct{ Path, Branch, RemoteSHA string }

// fakeWorktrees fakes creating, checking out, merging, and pushing, but commits with real git in a scratch repo.
type fakeWorktrees struct {
	t        *testing.T
	root     string
	real     *git.Git
	mu       sync.Mutex
	order    []string
	created  []string
	checked  []string
	pushes   []pushCall
	merges   []string
	removed  []string
	conflict []string
	newSHA   string
	onPush   func()
	branches []string
	deleted  []string
	pushErr  error
	// realSHA reads HEAD from the scratch repo instead of returning a fixed sha.
	realSHA bool
}

func newFakeWorktrees(t *testing.T, root string) *fakeWorktrees {
	return &fakeWorktrees{t: t, root: root, real: git.New(log.Discard()), newSHA: ""}
}

func (f *fakeWorktrees) record(event string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.order = append(f.order, event)
}

func (f *fakeWorktrees) scratchRepo(branch string) string {
	path := filepath.Join(f.root, ".worktrees", branch)
	if err := os.MkdirAll(path, 0o755); err != nil {
		f.t.Fatal(err)
	}
	gittest.Run(f.t, path, "init", "-q")
	gittest.Run(f.t, path, "config", "user.email", "test@example.com")
	gittest.Run(f.t, path, "config", "user.name", "test")
	gittest.Run(f.t, path, "commit", "-q", "--allow-empty", "-m", "init")
	return path
}

func (f *fakeWorktrees) CreateWorktree(_ context.Context, _, slug, _, _ string) (git.Session, error) {
	f.created = append(f.created, slug)
	branch := "vteam/test-agent-run"
	return git.Session{Path: f.scratchRepo(branch), Branch: branch, RemoteSHA: f.newSHA}, nil
}

func (f *fakeWorktrees) CheckoutWorktree(_ context.Context, root, branch, dir string) (git.Session, error) {
	f.checked = append(f.checked, root+"|"+branch+"|"+dir)
	return git.Session{Path: f.scratchRepo(branch), Branch: branch, RemoteSHA: "pr-head-sha"}, nil
}

func (f *fakeWorktrees) RemoveWorktree(ctx context.Context, _, path string) {
	if ctx.Err() != nil {
		return
	}
	f.removed = append(f.removed, path)
}

func (f *fakeWorktrees) MergeBaseBranch(_ context.Context, wt, base string) ([]string, error) {
	f.merges = append(f.merges, wt+"|"+base)
	return f.conflict, nil
}

func (f *fakeWorktrees) Push(ctx context.Context, path, branch, remoteSHA string) error {
	if f.onPush != nil {
		f.onPush()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.pushErr != nil {
		return f.pushErr
	}
	f.record("push")
	f.pushes = append(f.pushes, pushCall{path, branch, remoteSHA})
	return nil
}

func (f *fakeWorktrees) CommitSHA(ctx context.Context, dir string) (string, error) {
	if f.realSHA {
		return f.real.CommitSHA(ctx, dir)
	}
	return "abc1234def", nil
}

func (f *fakeWorktrees) CreateBranch(_ context.Context, _, branch, remote string) (string, error) {
	f.branches = append(f.branches, branch+"<-"+remote)
	return "remote-" + remote + "-sha", nil
}

func (f *fakeWorktrees) DeleteBranch(_ context.Context, _, branch string) error {
	f.deleted = append(f.deleted, branch)
	return nil
}

type fakeWorkmux struct {
	git       *fakeWorktrees
	added     []string
	opened    []workmux.OpenOptions
	removed   []string
	list      []workmux.Worktree
	openErr   error
	removeErr error
}

func (f *fakeWorkmux) Add(_ context.Context, _, branch string) (workmux.Worktree, error) {
	f.added = append(f.added, branch)
	handle := strings.ReplaceAll(branch, "/", "-")
	return workmux.Worktree{Handle: handle, Branch: branch, Path: f.git.scratchRepo(handle)}, nil
}

func (f *fakeWorkmux) Open(_ context.Context, _ string, o workmux.OpenOptions) error {
	f.opened = append(f.opened, o)
	return f.openErr
}

func (f *fakeWorkmux) Remove(_ context.Context, _, handle string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, handle)
	return nil
}

func (f *fakeWorkmux) List(context.Context, string) ([]workmux.Worktree, error) { return f.list, nil }

func (f *fakeWorktrees) HasUncommittedChanges(ctx context.Context, dir string) bool {
	return f.real.HasUncommittedChanges(ctx, dir)
}

func (f *fakeWorktrees) CommitAll(ctx context.Context, dir, subject, body string) error {
	return f.real.CommitAll(ctx, dir, subject, body)
}

type replyCall struct {
	Number         int
	ThreadID, Body string
}

type fakePlatform struct {
	mu        sync.Mutex
	order     *[]string
	pr        *model.PullRequestInfo
	reviewPR  *model.ReviewablePR
	comments  []model.ReviewComment
	threads   []model.ReviewThread
	prURL     string
	createErr error
	replyErr  error

	prLookups []string
	created   []platform.PRRequest
	replies   []replyCall
	resolved  []string
	posted    []string
	unlabeled []string
}

func (f *fakePlatform) FindReviewablePR(context.Context, []string) (*model.ReviewablePR, error) {
	return f.reviewPR, nil
}
func (f *fakePlatform) ReviewComments(context.Context, int) ([]model.ReviewComment, error) {
	return f.comments, nil
}
func (f *fakePlatform) RepoSlug(context.Context) (string, error) { return "org/repo", nil }
func (f *fakePlatform) PullRequest(_ context.Context, url string) (model.PullRequestInfo, error) {
	f.prLookups = append(f.prLookups, url)
	if f.pr == nil {
		return model.PullRequestInfo{}, errors.New("no PR configured")
	}
	return *f.pr, nil
}
func (f *fakePlatform) OpenReviewThreads(context.Context, int) ([]model.ReviewThread, error) {
	return f.threads, nil
}
func (f *fakePlatform) ReplyToThread(_ context.Context, number int, id, body string) error {
	if f.order != nil {
		*f.order = append(*f.order, "reply")
	}
	if f.replyErr != nil {
		return f.replyErr
	}
	f.replies = append(f.replies, replyCall{number, id, body})
	return nil
}
func (f *fakePlatform) ResolveThread(_ context.Context, _ int, id string) error {
	f.resolved = append(f.resolved, id)
	return nil
}
func (f *fakePlatform) PostComment(_ context.Context, _ int, body string) error {
	f.posted = append(f.posted, body)
	return nil
}
func (f *fakePlatform) RemoveLabel(_ context.Context, _ int, label string) error {
	f.unlabeled = append(f.unlabeled, label)
	return nil
}
func (f *fakePlatform) CreatePR(_ context.Context, req platform.PRRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, req)
	if f.createErr != nil {
		return "", f.createErr
	}
	if f.prURL == "" {
		return "https://github.com/org/repo/pull/99", nil
	}
	return f.prURL, nil
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	return gittest.Run(t, dir, args...)
}
