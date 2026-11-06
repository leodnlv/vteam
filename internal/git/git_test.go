package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/execx"
	"github.com/leodnlv/vteam/internal/git/gittest"
	"github.com/leodnlv/vteam/internal/log"
)

const wtDir = ".vteam-worktrees"

var ctx = context.Background()

func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	os.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	os.Exit(m.Run())
}

// newParallelGit marks the test parallel, since every test works in its own temp repositories.
func newParallelGit(t *testing.T) *Git {
	t.Parallel()
	g := New(log.Discard())
	g.Sleep = func(context.Context, time.Duration) error { return nil }
	return g
}

func must[T any](v T, err error) func(*testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func TestCreateWorktree(t *testing.T) {
	t.Run("creates a worktree from scratch", func(t *testing.T) {
		g := newParallelGit(t)
		local, _ := gittest.InitWithOrigin(t)
		wt := must(g.CreateWorktree(ctx, local, "my-task", "main", wtDir))(t)
		if wt.Branch != "vteam/my-task" || wt.RemoteSHA != "" || !gittest.Exists(filepath.Join(wt.Path, "file.txt")) {
			t.Errorf("session = %+v", wt)
		}
	})

	t.Run("slugifies the branch name", func(t *testing.T) {
		g := newParallelGit(t)
		local, _ := gittest.InitWithOrigin(t)
		wt := must(g.CreateWorktree(ctx, local, "Fix SQL Injection!-20260101-coder", "main", wtDir))(t)
		if wt.Branch != "vteam/fix-sql-injection-20260101-coder" {
			t.Errorf("branch = %q", wt.Branch)
		}
	})

	t.Run("succeeds when worktree already exists at the same path", func(t *testing.T) {
		g := newParallelGit(t)
		local, _ := gittest.InitWithOrigin(t)
		must(g.CreateWorktree(ctx, local, "my-task", "main", wtDir))(t)
		wt := must(g.CreateWorktree(ctx, local, "my-task", "main", wtDir))(t)
		if !gittest.Exists(wt.Path) || wt.Branch != "vteam/my-task" {
			t.Errorf("session = %+v", wt)
		}
	})

	t.Run("succeeds when orphaned directory exists without git metadata", func(t *testing.T) {
		g := newParallelGit(t)
		local, _ := gittest.InitWithOrigin(t)
		gittest.WriteFile(t, filepath.Join(local, wtDir, "vteam/my-task"), "stale.txt", "leftover")
		wt := must(g.CreateWorktree(ctx, local, "my-task", "main", wtDir))(t)
		if !gittest.Exists(filepath.Join(wt.Path, "file.txt")) || gittest.Exists(filepath.Join(wt.Path, "stale.txt")) {
			t.Error("stale directory was not replaced")
		}
	})

	t.Run("succeeds when git metadata is stale (directory manually deleted)", func(t *testing.T) {
		g := newParallelGit(t)
		local, _ := gittest.InitWithOrigin(t)
		first := must(g.CreateWorktree(ctx, local, "my-task", "main", wtDir))(t)
		_ = os.RemoveAll(first.Path)
		wt := must(g.CreateWorktree(ctx, local, "my-task", "main", wtDir))(t)
		if !gittest.Exists(wt.Path) || wt.Branch != "vteam/my-task" {
			t.Errorf("session = %+v", wt)
		}
	})

	t.Run("branches from the latest remote base branch when local main is behind", func(t *testing.T) {
		g := newParallelGit(t)
		local, origin := gittest.InitWithOrigin(t)
		gittest.Commit(t, origin, "upstream.txt", "x\n")
		wt := must(g.CreateWorktree(ctx, local, "my-task", "main", wtDir))(t)
		if !gittest.Exists(filepath.Join(wt.Path, "upstream.txt")) {
			t.Error("upstream commit missing")
		}
	})

	t.Run("leaves out unpushed commits on local main", func(t *testing.T) {
		g := newParallelGit(t)
		local, _ := gittest.InitWithOrigin(t)
		gittest.Commit(t, local, "local-only.txt", "x\n")
		wt := must(g.CreateWorktree(ctx, local, "my-task", "main", wtDir))(t)
		if gittest.Exists(filepath.Join(wt.Path, "local-only.txt")) {
			t.Error("unpushed local commit included")
		}
	})
}

func TestCheckoutWorktree(t *testing.T) {
	t.Run("checks out a remote branch into a worktree", func(t *testing.T) {
		g := newParallelGit(t)
		local, origin := gittest.InitWithOrigin(t)
		wt := must(g.CheckoutWorktree(ctx, local, "feature-branch", wtDir))(t)
		if wt.Branch != "feature-branch" || !gittest.Exists(filepath.Join(wt.Path, "feature.txt")) {
			t.Errorf("session = %+v", wt)
		}
		if want := gittest.Run(t, origin, "rev-parse", "feature-branch"); wt.RemoteSHA != want {
			t.Errorf("remote sha = %q, want %q", wt.RemoteSHA, want)
		}
	})

	t.Run("succeeds when worktree already exists at the same path", func(t *testing.T) {
		g := newParallelGit(t)
		local, _ := gittest.InitWithOrigin(t)
		must(g.CheckoutWorktree(ctx, local, "feature-branch", wtDir))(t)
		if wt := must(g.CheckoutWorktree(ctx, local, "feature-branch", wtDir))(t); !gittest.Exists(wt.Path) {
			t.Error("worktree missing")
		}
	})

	t.Run("succeeds when orphaned directory exists without git metadata", func(t *testing.T) {
		g := newParallelGit(t)
		local, _ := gittest.InitWithOrigin(t)
		gittest.WriteFile(t, filepath.Join(local, wtDir, "feature-branch"), "stale.txt", "leftover")
		wt := must(g.CheckoutWorktree(ctx, local, "feature-branch", wtDir))(t)
		if !gittest.Exists(filepath.Join(wt.Path, "feature.txt")) || gittest.Exists(filepath.Join(wt.Path, "stale.txt")) {
			t.Error("stale directory was not replaced")
		}
	})

	t.Run("succeeds when git metadata is stale (directory manually deleted)", func(t *testing.T) {
		g := newParallelGit(t)
		local, _ := gittest.InitWithOrigin(t)
		first := must(g.CheckoutWorktree(ctx, local, "feature-branch", wtDir))(t)
		_ = os.RemoveAll(first.Path)
		if wt := must(g.CheckoutWorktree(ctx, local, "feature-branch", wtDir))(t); !gittest.Exists(wt.Path) {
			t.Error("worktree missing")
		}
	})

	t.Run("succeeds when branch is already checked out in another worktree", func(t *testing.T) {
		g := newParallelGit(t)
		local, _ := gittest.InitWithOrigin(t)
		gittest.Run(t, local, "fetch", "-q", "origin", "feature-branch")
		gittest.Run(t, local, "worktree", "add", "-q", filepath.Join(local, "dev-worktree"), "origin/feature-branch", "-b", "feature-branch")
		wt := must(g.CheckoutWorktree(ctx, local, "feature-branch", wtDir))(t)
		if !gittest.Exists(filepath.Join(wt.Path, "feature.txt")) {
			t.Error("feature.txt missing")
		}
	})

	t.Run("resets to latest remote state when local branch exists", func(t *testing.T) {
		g := newParallelGit(t)
		local, _ := gittest.InitWithOrigin(t)
		gittest.Run(t, local, "fetch", "-q", "origin", "feature-branch")
		gittest.Run(t, local, "branch", "feature-branch", "origin/feature-branch")
		wt := must(g.CheckoutWorktree(ctx, local, "feature-branch", wtDir))(t)
		if msg := gittest.Run(t, wt.Path, "log", "--format=%s", "-1"); msg != "feature.txt" {
			t.Errorf("HEAD = %q", msg)
		}
	})
}

type mergeRepo struct {
	clone string
}

func newMergeRepo(t *testing.T) mergeRepo {
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin.git")
	clone := filepath.Join(tmp, "clone")
	gittest.Run(t, tmp, "init", "-q", "--bare", "-b", "main", origin)
	gittest.Run(t, tmp, "clone", "-q", origin, clone)
	gittest.Run(t, clone, "config", "user.email", "test@test.com")
	gittest.Run(t, clone, "config", "user.name", "Test")
	gittest.Run(t, clone, "checkout", "-q", "-b", "main")
	gittest.Commit(t, clone, "thing.ts", "one\n")
	gittest.Commit(t, clone, "other.ts", "a\n")
	gittest.Run(t, clone, "push", "-q", "origin", "main")
	gittest.Run(t, clone, "checkout", "-q", "-b", "feature")
	gittest.Commit(t, clone, "thing.ts", "feature\n")
	return mergeRepo{clone: clone}
}

func (r mergeRepo) advanceMain(t *testing.T, file, content string) {
	gittest.Run(t, r.clone, "checkout", "-q", "main")
	gittest.Commit(t, r.clone, file, content)
	gittest.Run(t, r.clone, "push", "-q", "origin", "main")
	gittest.Run(t, r.clone, "checkout", "-q", "feature")
}

func (r mergeRepo) parentCount(t *testing.T) int {
	return len(strings.Fields(gittest.Run(t, r.clone, "rev-list", "--parents", "-n", "1", "HEAD"))) - 1
}

func TestMergeBaseBranch(t *testing.T) {
	t.Run("merges the base branch and reports no conflicts when it applies cleanly", func(t *testing.T) {
		g := newParallelGit(t)
		r := newMergeRepo(t)
		r.advanceMain(t, "other.ts", "b\n")
		conflicted := must(g.MergeBaseBranch(ctx, r.clone, "main"))(t)
		if len(conflicted) != 0 || r.parentCount(t) != 2 {
			t.Errorf("conflicted %v parents %d", conflicted, r.parentCount(t))
		}
	})

	t.Run("leaves the merge in progress and lists conflicted files, which commit as a merge once resolved", func(t *testing.T) {
		g := newParallelGit(t)
		r := newMergeRepo(t)
		r.advanceMain(t, "thing.ts", "main\n")
		conflicted := must(g.MergeBaseBranch(ctx, r.clone, "main"))(t)
		if diff := cmp.Diff([]string{"thing.ts"}, conflicted); diff != "" {
			t.Fatal(diff)
		}
		if diff := cmp.Diff([]string{"thing.ts"}, FindUnresolvedConflicts(r.clone, conflicted)); diff != "" {
			t.Error(diff)
		}
		gittest.WriteFile(t, r.clone, "thing.ts", "feature and main\n")
		if got := FindUnresolvedConflicts(r.clone, conflicted); len(got) != 0 {
			t.Errorf("unresolved = %v", got)
		}
		if err := g.CommitAll(ctx, r.clone, "vteam: resolve conflicts", "Merged main."); err != nil {
			t.Fatal(err)
		}
		if r.parentCount(t) != 2 {
			t.Error("resolution did not commit as a merge")
		}
	})

	t.Run("returns the merge error when it fails without conflicts", func(t *testing.T) {
		g := newParallelGit(t)
		r := newMergeRepo(t)
		if _, err := g.MergeBaseBranch(ctx, r.clone, "no-such-branch"); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestFindUnresolvedConflictsIgnoresLookalikes(t *testing.T) {
	dir := t.TempDir()
	gittest.WriteFile(t, dir, "doc.md", "Title\n=======\n<<<<<<<not a marker\n")
	if got := FindUnresolvedConflicts(dir, []string{"doc.md", "deleted.ts"}); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

func installPrePushHook(t *testing.T, repo, script string) {
	hooks := gittest.Run(t, repo, "rev-parse", "--git-path", "hooks")
	if !filepath.IsAbs(hooks) {
		hooks = filepath.Join(repo, hooks)
	}
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-push"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

var leaseRejected = regexp.MustCompile(`stale info|rejected`)

func TestPush(t *testing.T) {
	t.Run("pushes a new branch", func(t *testing.T) {
		g := newParallelGit(t)
		local, origin := gittest.InitWithOrigin(t)
		wt := must(g.CreateWorktree(ctx, local, "new-task", "main", wtDir))(t)
		head := gittest.Commit(t, wt.Path, "change.txt", "x\n")
		if err := g.Push(ctx, wt.Path, wt.Branch, wt.RemoteSHA); err != nil {
			t.Fatal(err)
		}
		if got := gittest.Run(t, origin, "rev-parse", wt.Branch); got != head {
			t.Errorf("origin = %s, want %s", got, head)
		}
	})

	t.Run("refuses to overwrite a new branch that already exists on the remote", func(t *testing.T) {
		g := newParallelGit(t)
		g.RetryDelays = nil
		local, origin := gittest.InitWithOrigin(t)
		wt := must(g.CreateWorktree(ctx, local, "new-task", "main", wtDir))(t)
		gittest.Commit(t, wt.Path, "change.txt", "x\n")
		gittest.Run(t, origin, "branch", wt.Branch, "feature-branch")
		err := g.Push(ctx, wt.Path, wt.Branch, wt.RemoteSHA)
		if err == nil || !leaseRejected.MatchString(err.Error()) {
			t.Fatalf("err = %v", err)
		}
		if gittest.Run(t, origin, "rev-parse", wt.Branch) != gittest.Run(t, origin, "rev-parse", "feature-branch") {
			t.Error("remote branch was overwritten")
		}
	})

	t.Run("pushes to an existing PR branch", func(t *testing.T) {
		g := newParallelGit(t)
		local, origin := gittest.InitWithOrigin(t)
		wt := must(g.CheckoutWorktree(ctx, local, "feature-branch", wtDir))(t)
		head := gittest.Commit(t, wt.Path, "fix.txt", "x\n")
		if err := g.Push(ctx, wt.Path, wt.Branch, wt.RemoteSHA); err != nil {
			t.Fatal(err)
		}
		if got := gittest.Run(t, origin, "rev-parse", "feature-branch"); got != head {
			t.Errorf("origin = %s, want %s", got, head)
		}
	})

	t.Run("refuses to overwrite commits pushed to the PR branch after checkout", func(t *testing.T) {
		g := newParallelGit(t)
		g.RetryDelays = nil
		local, origin := gittest.InitWithOrigin(t)
		wt := must(g.CheckoutWorktree(ctx, local, "feature-branch", wtDir))(t)
		gittest.Commit(t, wt.Path, "fix.txt", "x\n")
		gittest.Run(t, origin, "checkout", "-q", "feature-branch")
		human := gittest.Commit(t, origin, "human.txt", "x\n")
		gittest.Run(t, origin, "checkout", "-q", "main")
		err := g.Push(ctx, wt.Path, wt.Branch, wt.RemoteSHA)
		if err == nil || !leaseRejected.MatchString(err.Error()) {
			t.Fatalf("err = %v", err)
		}
		if got := gittest.Run(t, origin, "rev-parse", "feature-branch"); got != human {
			t.Error("human commit was overwritten")
		}
	})

	t.Run("retries after a network error", func(t *testing.T) {
		g := newParallelGit(t)
		g.RetryDelays = []time.Duration{0}
		local, origin := gittest.InitWithOrigin(t)
		wt := must(g.CreateWorktree(ctx, local, "new-task", "main", wtDir))(t)
		head := gittest.Commit(t, wt.Path, "change.txt", "x\n")
		marker := filepath.Join(local, "push-failed-once")
		installPrePushHook(t, local, `[ -f "`+marker+`" ] && exit 0
touch "`+marker+`"
echo "Read from remote host: Connection reset by peer" >&2
exit 1`)
		if err := g.Push(ctx, wt.Path, wt.Branch, wt.RemoteSHA); err != nil {
			t.Fatal(err)
		}
		if got := gittest.Run(t, origin, "rev-parse", wt.Branch); got != head {
			t.Errorf("origin = %s", got)
		}
	})

	t.Run("treats a dropped connection as success when the push already landed", func(t *testing.T) {
		g := newParallelGit(t)
		g.RetryDelays = []time.Duration{0}
		local, origin := gittest.InitWithOrigin(t)
		wt := must(g.CreateWorktree(ctx, local, "new-task", "main", wtDir))(t)
		head := gittest.Commit(t, wt.Path, "change.txt", "x\n")
		installPrePushHook(t, local, `git push --no-verify origin "HEAD:refs/heads/`+wt.Branch+`" 2>/dev/null
echo "fatal: the remote end hung up unexpectedly" >&2
exit 1`)
		if err := g.Push(ctx, wt.Path, wt.Branch, wt.RemoteSHA); err != nil {
			t.Fatal(err)
		}
		if got := gittest.Run(t, origin, "rev-parse", wt.Branch); got != head {
			t.Errorf("origin = %s", got)
		}
	})

	t.Run("gives up after the last retry", func(t *testing.T) {
		g := newParallelGit(t)
		g.RetryDelays = []time.Duration{0, 0}
		local, _ := gittest.InitWithOrigin(t)
		wt := must(g.CreateWorktree(ctx, local, "new-task", "main", wtDir))(t)
		gittest.Commit(t, wt.Path, "change.txt", "x\n")
		installPrePushHook(t, local, `echo "client_loop: send disconnect: Broken pipe" >&2
exit 1`)
		if err := g.Push(ctx, wt.Path, wt.Branch, wt.RemoteSHA); err == nil || !strings.Contains(err.Error(), "Broken pipe") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("waits the configured delays between attempts", func(t *testing.T) {
		var waited []time.Duration
		fake := &execx.Fake{Handler: func(c execx.Cmd) (string, error) {
			if c.Args[0] == "push" {
				return "", execx.Failure(c, "fatal: the remote end hung up unexpectedly")
			}
			if c.Args[0] == "ls-remote" {
				return "def\trefs/heads/b\n", nil
			}
			return "abc\n", nil
		}}
		g := &Git{Exec: fake, Log: log.Discard(), RetryDelays: []time.Duration{5 * time.Second, 15 * time.Second},
			Sleep: func(_ context.Context, d time.Duration) error { waited = append(waited, d); return nil }}
		if err := g.Push(ctx, "/wt", "b", ""); err == nil {
			t.Fatal("expected failure")
		}
		if diff := cmp.Diff([]time.Duration{5 * time.Second, 15 * time.Second}, waited); diff != "" {
			t.Error(diff)
		}
	})
}

func TestIsTransientPushError(t *testing.T) {
	t.Run("recognises network failures from git push", func(t *testing.T) {
		err := &execx.Error{Cmd: execx.Cmd{Name: "git", Args: []string{"push"}},
			Stderr: "send-pack: unexpected disconnect while reading sideband packet\nfatal: the remote end hung up unexpectedly"}
		if !IsTransientPushError(err) {
			t.Error("not transient")
		}
	})
	t.Run("does not retry lease rejections", func(t *testing.T) {
		err := &execx.Error{Cmd: execx.Cmd{Name: "git", Args: []string{"push"}},
			Stderr: " ! [rejected]        HEAD -> feature (stale info)\nerror: failed to push some refs"}
		if IsTransientPushError(err) {
			t.Error("transient")
		}
	})
	t.Run("handles nil and plain errors", func(t *testing.T) {
		if IsTransientPushError(nil) || !IsTransientPushError(errors.New("dial tcp: i/o timed out")) {
			t.Error("wrong classification")
		}
	})
}

func TestCleanupOrphaned(t *testing.T) {
	g := newParallelGit(t)
	repo := gittest.Init(t)
	busy := filepath.Join(repo, wtDir, "vteam", "busy")
	idle := filepath.Join(repo, wtDir, "vteam", "idle")
	outside := filepath.Join(t.TempDir(), "outside")
	gittest.Run(t, repo, "worktree", "add", "-q", "-b", "vteam/busy", busy)
	gittest.Run(t, repo, "worktree", "add", "-q", "-b", "vteam/idle", idle)
	gittest.Run(t, repo, "worktree", "add", "-q", "-b", "outside", outside)

	orphaned := must(g.FindOrphaned(ctx, repo, wtDir, []string{busy}))(t)
	if len(orphaned) != 1 || !strings.HasSuffix(orphaned[0], "vteam/idle") {
		t.Fatalf("orphaned = %v", orphaned)
	}
	cleaned := must(g.CleanupOrphaned(ctx, repo, wtDir, []string{busy}))(t)
	if len(cleaned) != 1 || gittest.Exists(idle) || !gittest.Exists(busy) || !gittest.Exists(outside) {
		t.Errorf("cleaned = %v", cleaned)
	}
}

func TestListWorktreesAndHelpers(t *testing.T) {
	g := newParallelGit(t)
	repo := gittest.Init(t)
	worktrees := must(g.ListWorktrees(ctx, repo))(t)
	if len(worktrees) != 1 || worktrees[0].Branch != "refs/heads/main" {
		t.Errorf("worktrees = %+v", worktrees)
	}
	if g.HasUncommittedChanges(ctx, repo) {
		t.Error("clean repo reported dirty")
	}
	gittest.WriteFile(t, repo, "new.txt", "x")
	if !g.HasUncommittedChanges(ctx, repo) {
		t.Error("dirty repo reported clean")
	}
	if err := g.CommitAll(ctx, repo, "subject", "body"); err != nil {
		t.Fatal(err)
	}
	if msg := gittest.Run(t, repo, "log", "-1", "--format=%B"); msg != "subject\n\nbody" {
		t.Errorf("message = %q", msg)
	}
	if g.HasUncommittedChanges(ctx, t.TempDir()) {
		t.Error("non-repo reported dirty")
	}
	g.RemoveWorktree(ctx, repo, filepath.Join(repo, "missing"))
}

func TestCreateBranch(t *testing.T) {
	g := newParallelGit(t)
	local, origin := gittest.InitWithOrigin(t)
	want := strings.TrimSpace(gittest.Run(t, origin, "rev-parse", "feature-branch"))
	sha := must(g.CreateBranch(ctx, local, "vteam-session/run-1", "feature-branch"))(t)
	if sha != want {
		t.Errorf("sha = %q, want %q", sha, want)
	}
	if got := strings.TrimSpace(gittest.Run(t, local, "rev-parse", "vteam-session/run-1")); got != want {
		t.Errorf("branch at %q", got)
	}
	if upstream, err := g.run(ctx, local, "rev-parse", "--abbrev-ref", "vteam-session/run-1@{upstream}"); err == nil {
		t.Errorf("branch tracks %q", upstream)
	}
	if _, err := g.CreateBranch(ctx, local, "vteam-session/run-1", "feature-branch"); err == nil {
		t.Error("expected an error for an existing branch")
	}
}

func TestDeleteBranch(t *testing.T) {
	g := newParallelGit(t)
	local, _ := gittest.InitWithOrigin(t)
	must(g.CreateBranch(ctx, local, "vteam/gone", "main"))(t)
	if err := g.DeleteBranch(ctx, local, "vteam/gone"); err != nil {
		t.Fatal(err)
	}
	if out := gittest.Run(t, local, "branch", "--list", "vteam/gone"); strings.TrimSpace(out) != "" {
		t.Errorf("branch still there: %q", out)
	}
}
