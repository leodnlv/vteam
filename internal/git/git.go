// Package git manages the worktrees agents run in and pushes their branches.
package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/leodnlv/vteam/internal/execx"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/slugify"
)

type Session struct {
	Path   string
	Branch string
	// RemoteSHA is the branch's commit on origin when the session started, "" for a new branch.
	// Pushes use it as the --force-with-lease expectation.
	RemoteSHA string
}

type Worktree struct {
	Path   string
	Branch string
}

type Git struct {
	Exec        execx.Runner
	Log         *log.Logger
	Sleep       func(ctx context.Context, d time.Duration) error
	RetryDelays []time.Duration
}

func New(logger *log.Logger) *Git {
	return &Git{Exec: execx.OS{}, Log: logger, Sleep: sleep, RetryDelays: []time.Duration{5 * time.Second, 15 * time.Second}}
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func (g *Git) run(ctx context.Context, dir string, args ...string) (string, error) {
	return g.Exec.Run(ctx, execx.Cmd{Name: "git", Args: args, Dir: dir})
}

func (g *Git) CreateWorktree(ctx context.Context, repoRoot, taskSlug, baseBranch, worktreeDir string) (Session, error) {
	branch := "vteam/" + slugify.Slugify(taskSlug)
	path := filepath.Join(repoRoot, worktreeDir, branch)
	if err := g.ensureCleanPath(ctx, repoRoot, path); err != nil {
		return Session{}, err
	}
	_, _ = g.run(ctx, repoRoot, "branch", "-D", branch)
	if _, err := g.run(ctx, repoRoot, "fetch", "origin", baseBranch); err != nil {
		return Session{}, err
	}
	if _, err := g.run(ctx, repoRoot, "worktree", "add", "--no-track", "-b", branch, path, "origin/"+baseBranch); err != nil {
		return Session{}, err
	}
	return Session{Path: path, Branch: branch}, nil
}

func (g *Git) CheckoutWorktree(ctx context.Context, repoRoot, remoteBranch, worktreeDir string) (Session, error) {
	path := filepath.Join(repoRoot, worktreeDir, remoteBranch)
	if err := g.ensureCleanPath(ctx, repoRoot, path); err != nil {
		return Session{}, err
	}
	if _, err := g.run(ctx, repoRoot, "fetch", "origin", remoteBranch); err != nil {
		return Session{}, err
	}
	if _, err := g.run(ctx, repoRoot, "worktree", "add", "--detach", path, "origin/"+remoteBranch); err != nil {
		return Session{}, err
	}
	sha, err := g.CommitSHA(ctx, path)
	if err != nil {
		return Session{}, err
	}
	return Session{Path: path, Branch: remoteBranch, RemoteSHA: sha}, nil
}

// CreateBranch fetches origin/<remoteBranch> and creates the local branch at it, without tracking, for a worktree
// someone else (workmux) creates. It returns the commit the branch starts at.
func (g *Git) CreateBranch(ctx context.Context, repoRoot, branch, remoteBranch string) (string, error) {
	if _, err := g.run(ctx, repoRoot, "fetch", "origin", remoteBranch); err != nil {
		return "", err
	}
	if _, err := g.run(ctx, repoRoot, "branch", "--no-track", branch, "origin/"+remoteBranch); err != nil {
		return "", err
	}
	out, err := g.run(ctx, repoRoot, "rev-parse", "origin/"+remoteBranch)
	return strings.TrimSpace(out), err
}

// MergeBaseBranch merges origin/<base> into the worktree. On conflicts it leaves the merge in progress
// and returns the conflicted files; MERGE_HEAD makes the next commit the merge commit.
func (g *Git) MergeBaseBranch(ctx context.Context, worktree, baseBranch string) ([]string, error) {
	if _, err := g.run(ctx, worktree, "fetch", "origin", baseBranch); err != nil {
		return nil, err
	}
	_, mergeErr := g.run(ctx, worktree, "merge", "--no-edit", "origin/"+baseBranch)
	if mergeErr == nil {
		return nil, nil
	}
	out, err := g.run(ctx, worktree, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	conflicted := strings.Fields(out)
	if len(conflicted) == 0 {
		return nil, mergeErr
	}
	return conflicted, nil
}

var conflictMarker = regexp.MustCompile(`(?m)^(<{7}|>{7})( |$)`)

func FindUnresolvedConflicts(worktree string, files []string) []string {
	var unresolved []string
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(worktree, f))
		if err == nil && conflictMarker.Match(data) {
			unresolved = append(unresolved, f)
		}
	}
	return unresolved
}

func (g *Git) ensureCleanPath(ctx context.Context, repoRoot, path string) error {
	if _, err := g.run(ctx, repoRoot, "worktree", "remove", "--force", path); err == nil {
		return nil
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	_, err := g.run(ctx, repoRoot, "worktree", "prune")
	return err
}

// DeleteBranch deletes a local branch, merged or not.
func (g *Git) DeleteBranch(ctx context.Context, repoRoot, branch string) error {
	_, err := g.run(ctx, repoRoot, "branch", "-D", branch)
	return err
}

func (g *Git) RemoveWorktree(ctx context.Context, repoRoot, path string) {
	if _, err := g.run(ctx, repoRoot, "worktree", "remove", path, "--force"); err == nil {
		return
	}
	if _, err := os.Stat(path); err == nil {
		_ = os.RemoveAll(path)
		_, _ = g.run(ctx, repoRoot, "worktree", "prune")
	}
}

func (g *Git) ListWorktrees(ctx context.Context, repoRoot string) ([]Worktree, error) {
	out, err := g.run(ctx, repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var worktrees []Worktree
	var current string
	for _, line := range strings.Split(out, "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			current = path
		} else if branch, ok := strings.CutPrefix(line, "branch "); ok {
			worktrees = append(worktrees, Worktree{Path: current, Branch: branch})
		}
	}
	return worktrees, nil
}

func canonicalPath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	abs, _ := filepath.Abs(path)
	return abs
}

// FindOrphaned lists worktrees under worktreeDir that no path in inUse refers to. Paths compare by real path.
func (g *Git) FindOrphaned(ctx context.Context, repoRoot, worktreeDir string, inUse []string) ([]string, error) {
	base := canonicalPath(filepath.Join(repoRoot, worktreeDir)) + string(filepath.Separator)
	kept := map[string]bool{}
	for _, p := range inUse {
		kept[canonicalPath(p)] = true
	}
	worktrees, err := g.ListWorktrees(ctx, repoRoot)
	if err != nil {
		return nil, err
	}
	var orphaned []string
	for _, wt := range worktrees {
		real := canonicalPath(wt.Path)
		if strings.HasPrefix(real, base) && !kept[real] {
			orphaned = append(orphaned, wt.Path)
		}
	}
	return orphaned, nil
}

func (g *Git) CleanupOrphaned(ctx context.Context, repoRoot, worktreeDir string, inUse []string) ([]string, error) {
	orphaned, err := g.FindOrphaned(ctx, repoRoot, worktreeDir, inUse)
	if err != nil {
		return nil, err
	}
	for _, p := range orphaned {
		g.RemoveWorktree(ctx, repoRoot, p)
	}
	return orphaned, nil
}

var transientPushError = regexp.MustCompile(`(?i)connection reset|broken pipe|remote end hung up|unexpected disconnect|early EOF|RPC failed|timed out|could not resolve host|connection closed by|kex_exchange_identification`)

func IsTransientPushError(err error) bool {
	return err != nil && transientPushError.MatchString(err.Error())
}

func (g *Git) remoteBranchSHA(ctx context.Context, worktree, branch string) string {
	out, err := g.run(ctx, worktree, "ls-remote", "origin", "refs/heads/"+branch)
	if err != nil {
		return ""
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// Push pushes HEAD to origin/<branch>, leased on expectedRemoteSHA so commits someone else pushed
// during the run are never overwritten. Transient network errors are retried after RetryDelays.
func (g *Git) Push(ctx context.Context, worktree, branch, expectedRemoteSHA string) error {
	head, err := g.CommitSHA(ctx, worktree)
	if err != nil {
		return err
	}
	lease := fmt.Sprintf("--force-with-lease=refs/heads/%s:%s", branch, expectedRemoteSHA)
	for attempt := 0; ; attempt++ {
		_, err := g.run(ctx, worktree, "push", lease, "origin", "HEAD:refs/heads/"+branch)
		if err == nil {
			return nil
		}
		if attempt >= len(g.RetryDelays) || !IsTransientPushError(err) {
			return err
		}
		delay := g.RetryDelays[attempt]
		g.Log.Warn(fmt.Sprintf("push failed with a network error, retrying in %s", log.FormatDuration(delay)))
		if err := g.Sleep(ctx, delay); err != nil {
			return errors.Join(err, ctx.Err())
		}
		// A dropped connection can still land the push, and the lease would then reject the retry.
		if g.remoteBranchSHA(ctx, worktree, branch) == head {
			return nil
		}
	}
}

func (g *Git) CommitSHA(ctx context.Context, dir string) (string, error) {
	out, err := g.run(ctx, dir, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

func (g *Git) HasUncommittedChanges(ctx context.Context, dir string) bool {
	out, err := g.run(ctx, dir, "status", "--porcelain")
	return err == nil && strings.TrimSpace(out) != ""
}

func (g *Git) CommitAll(ctx context.Context, dir, subject, body string) error {
	if _, err := g.run(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	_, err := g.run(ctx, dir, "commit", "-m", subject, "-m", body)
	return err
}
