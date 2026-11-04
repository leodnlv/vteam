package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/execx"
	"github.com/leodnlv/vteam/internal/log"
)

// failing returns a Git whose runner fails every command that starts with one of prefixes.
func failing(prefixes ...string) (*Git, *execx.Fake) {
	fake := &execx.Fake{Handler: func(c execx.Cmd) (string, error) {
		line := strings.Join(c.Args, " ")
		for _, p := range prefixes {
			if strings.HasPrefix(line, p) {
				return "", execx.Failure(c, "fatal: "+p)
			}
		}
		return "sha\n", nil
	}}
	return &Git{Exec: fake, Log: log.Discard(), Sleep: sleep, RetryDelays: []time.Duration{time.Hour}}, fake
}

func TestErrorPaths(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cases := map[string]struct {
		g   *Git
		run func(g *Git) error
	}{}
	add := func(name string, fail []string, run func(g *Git) error) {
		g, _ := failing(fail...)
		cases[name] = struct {
			g   *Git
			run func(g *Git) error
		}{g, run}
	}
	add("create: fetch fails", []string{"fetch"}, func(g *Git) error {
		_, err := g.CreateWorktree(ctx, dir, "t", "main", ".wt")
		return err
	})
	add("create: worktree add fails", []string{"worktree add"}, func(g *Git) error {
		_, err := g.CreateWorktree(ctx, dir, "t", "main", ".wt")
		return err
	})
	add("create: prune fails", []string{"worktree remove", "worktree prune"}, func(g *Git) error {
		_, err := g.CreateWorktree(ctx, dir, "t", "main", ".wt")
		return err
	})
	add("checkout: fetch fails", []string{"fetch"}, func(g *Git) error {
		_, err := g.CheckoutWorktree(ctx, dir, "b", ".wt")
		return err
	})
	add("checkout: worktree add fails", []string{"worktree add"}, func(g *Git) error {
		_, err := g.CheckoutWorktree(ctx, dir, "b", ".wt")
		return err
	})
	add("checkout: rev-parse fails", []string{"rev-parse"}, func(g *Git) error {
		_, err := g.CheckoutWorktree(ctx, dir, "b", ".wt")
		return err
	})
	add("merge: fetch fails", []string{"fetch"}, func(g *Git) error {
		_, err := g.MergeBaseBranch(ctx, dir, "main")
		return err
	})
	add("merge: listing conflicts fails", []string{"merge", "diff"}, func(g *Git) error {
		_, err := g.MergeBaseBranch(ctx, dir, "main")
		return err
	})
	add("list fails", []string{"worktree list"}, func(g *Git) error {
		_, err := g.CleanupOrphaned(ctx, dir, ".wt", nil)
		return err
	})
	add("push: rev-parse fails", []string{"rev-parse"}, func(g *Git) error {
		return g.Push(ctx, dir, "b", "")
	})
	add("commit: add fails", []string{"add"}, func(g *Git) error {
		return g.CommitAll(ctx, dir, "s", "b")
	})
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var execErr *execx.Error
			if err := c.run(c.g); !errors.As(err, &execErr) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestPushStopsRetryingWhenCanceled(t *testing.T) {
	t.Parallel()
	g, _ := failing()
	g.Exec = &execx.Fake{Handler: func(c execx.Cmd) (string, error) {
		if c.Args[0] == "push" {
			return "", execx.Failure(c, "fatal: the remote end hung up unexpectedly")
		}
		return "sha\n", nil
	}}
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := g.Push(cctx, "/wt", "b", "")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestRemoveWorktreeFallsBackToDeletingTheDirectory(t *testing.T) {
	t.Parallel()
	g, fake := failing("worktree remove")
	path := filepath.Join(t.TempDir(), "wt")
	_ = os.MkdirAll(filepath.Join(path, "sub"), 0o755)
	g.RemoveWorktree(ctx, "/repo", path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("directory left behind")
	}
	if last := fake.Args()[len(fake.Args())-1]; !cmp.Equal([]string{"git", "worktree", "prune"}, last) {
		t.Errorf("last call = %v", last)
	}
}

func TestCanonicalPathOfAMissingPath(t *testing.T) {
	t.Parallel()
	if got := canonicalPath("/definitely/not/here"); got != "/definitely/not/here" {
		t.Errorf("got %q", got)
	}
}

func TestSleepHonorsTheContext(t *testing.T) {
	t.Parallel()
	if err := sleep(context.Background(), time.Millisecond); err != nil {
		t.Error(err)
	}
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleep(cctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}
