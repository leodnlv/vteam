package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/leodnlv/vteam/internal/git"
	"github.com/leodnlv/vteam/internal/git/gittest"
	"github.com/leodnlv/vteam/internal/log"
)

type fakeWorkmux struct {
	removed []string
	err     error
}

func (f *fakeWorkmux) Remove(_ context.Context, _, handle string) error {
	f.removed = append(f.removed, handle)
	return f.err
}

// abandonedSession adds a worktree on its own branch to repo and saves an abandoned session for it.
func abandonedSession(t *testing.T, repo, runID string) Session {
	t.Helper()
	path := filepath.Join(repo, ".worktrees", runID)
	branch := "vteam/" + runID
	gittest.Run(t, repo, "worktree", "add", "-q", "-b", branch, path)
	s := Session{
		RunID: runID, Agent: "coder", Status: Abandoned, Handle: runID, WorktreePath: path, Branch: branch,
		StartSHA: gittest.Run(t, path, "rev-parse", "HEAD"), StartedAt: "2026-11-20T10:00:00.000Z",
	}
	if err := Save(repo, s); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{PromptPath(repo, runID), SystemPromptPath(repo, runID), WorkmuxConfigPath(repo, runID)} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func cleaner(repo string, wm *fakeWorkmux) Cleaner {
	return Cleaner{Cwd: repo, Git: git.New(log.Discard()), Workmux: wm}
}

func TestLeftoversReportUnsavedWork(t *testing.T) {
	repo := gittest.Init(t)
	abandonedSession(t, repo, "clean")
	dirty := abandonedSession(t, repo, "dirty")
	gittest.WriteFile(t, dirty.WorktreePath, "wip.txt", "wip\n")
	committed := abandonedSession(t, repo, "committed")
	gittest.Commit(t, committed.WorktreePath, "done.txt", "done\n")
	gone := abandonedSession(t, repo, "gone")
	gittest.Run(t, repo, "worktree", "remove", gone.WorktreePath)
	if err := Save(repo, Session{RunID: "open", Status: Open, WorktreePath: "/w/open"}); err != nil {
		t.Fatal(err)
	}

	got := map[string]Leftover{}
	for _, l := range cleaner(repo, &fakeWorkmux{}).Leftovers(context.Background()) {
		got[l.RunID] = l
	}
	if len(got) != 4 {
		t.Fatalf("leftovers = %+v", got)
	}
	if l := got["clean"]; l.Unsaved() || l.Gone {
		t.Errorf("clean = %+v", l)
	}
	if l := got["dirty"]; !l.Uncommitted || l.Commits {
		t.Errorf("dirty = %+v", l)
	}
	if l := got["committed"]; l.Uncommitted || !l.Commits {
		t.Errorf("committed = %+v", l)
	}
	if l := got["gone"]; !l.Gone || l.Unsaved() {
		t.Errorf("gone = %+v", l)
	}
}

func TestCleanKeepsUnsavedWorkUnlessDiscarded(t *testing.T) {
	repo := gittest.Init(t)
	s := abandonedSession(t, repo, "clean")
	dirty := abandonedSession(t, repo, "dirty")
	gittest.WriteFile(t, dirty.WorktreePath, "wip.txt", "wip\n")
	wm := &fakeWorkmux{err: errors.New("workmux: unknown handle")}

	res := cleaner(repo, wm).Clean(context.Background(), false)
	if len(res.Removed) != 1 || res.Removed[0].RunID != "clean" || len(res.Kept) != 1 || res.Kept[0].RunID != "dirty" || len(res.Failed) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(s.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("worktree still there: %v", err)
	}
	if branches := gittest.Run(t, repo, "branch", "--list", s.Branch); branches != "" {
		t.Errorf("branch not deleted: %q", branches)
	}
	if _, err := os.Stat(PromptPath(repo, "clean")); !os.IsNotExist(err) {
		t.Errorf("prompt file still there: %v", err)
	}
	if got, err := Load(repo, "clean"); err != nil || got.Status != Removed {
		t.Errorf("session = %+v, %v", got, err)
	}
	if _, err := os.Stat(dirty.WorktreePath); err != nil {
		t.Errorf("dirty worktree removed: %v", err)
	}

	res = cleaner(repo, wm).Clean(context.Background(), true)
	if len(res.Removed) != 1 || res.Removed[0].RunID != "dirty" {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(dirty.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("dirty worktree still there: %v", err)
	}
	if diff := WorktreePaths(repo); len(diff) != 0 {
		t.Errorf("removed sessions still count as in use: %v", diff)
	}
}

func TestCleanMarksSessionsWithoutAWorktreeRemoved(t *testing.T) {
	repo := gittest.Init(t)
	gone := abandonedSession(t, repo, "gone")
	gittest.Run(t, repo, "worktree", "remove", gone.WorktreePath)
	wm := &fakeWorkmux{}

	res := cleaner(repo, wm).Clean(context.Background(), false)
	if len(res.Removed) != 1 || len(wm.removed) != 0 {
		t.Fatalf("result = %+v, workmux removed %v", res, wm.removed)
	}
	if got, _ := Load(repo, "gone"); got.Status != Removed {
		t.Errorf("status = %s", got.Status)
	}
}
