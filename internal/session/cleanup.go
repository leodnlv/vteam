package session

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Leftover is an abandoned session still to clean up, and what removing its worktree would throw away.
type Leftover struct {
	Session
	// Gone means the worktree no longer exists, so cleaning up only marks the session removed.
	Gone        bool
	Uncommitted bool
	// Commits means HEAD moved past StartSHA (or could not be read): commits made in the session that were never submitted.
	Commits bool
}

func (l Leftover) Unsaved() bool { return l.Uncommitted || l.Commits }

type CleanupGit interface {
	CommitSHA(ctx context.Context, dir string) (string, error)
	HasUncommittedChanges(ctx context.Context, dir string) bool
	RemoveWorktree(ctx context.Context, repoRoot, path string)
	DeleteBranch(ctx context.Context, repoRoot, branch string) error
}

type CleanupWorkmux interface {
	Remove(ctx context.Context, repoRoot, handle string) error
}

// Cleaner removes the worktrees and local branches of abandoned sessions.
type Cleaner struct {
	Cwd     string
	Git     CleanupGit
	Workmux CleanupWorkmux
}

type CleanupFailure struct {
	Leftover
	Err error
}

type CleanupResult struct {
	Removed []Leftover
	// Kept are sessions with unsaved work, left alone because the cleanup did not discard it.
	Kept   []Leftover
	Failed []CleanupFailure
}

// Leftovers lists the abandoned sessions, oldest first.
func (c Cleaner) Leftovers(ctx context.Context) []Leftover {
	var leftovers []Leftover
	for _, s := range List(c.Cwd) {
		if s.Status == Abandoned {
			leftovers = append(leftovers, c.inspect(ctx, s))
		}
	}
	return leftovers
}

func (c Cleaner) inspect(ctx context.Context, s Session) Leftover {
	l := Leftover{Session: s}
	if _, err := os.Stat(s.WorktreePath); err != nil {
		l.Gone = true
		return l
	}
	l.Uncommitted = c.Git.HasUncommittedChanges(ctx, s.WorktreePath)
	head, err := c.Git.CommitSHA(ctx, s.WorktreePath)
	l.Commits = err != nil || head != s.StartSHA
	return l
}

// Clean removes every abandoned session's worktree and local branch, deletes its prompt files, and marks it removed;
// session.json stays for the Overview's pipeline stats. Sessions with unsaved work are kept unless discardUnsaved.
func (c Cleaner) Clean(ctx context.Context, discardUnsaved bool) CleanupResult {
	var res CleanupResult
	for _, l := range c.Leftovers(ctx) {
		if l.Unsaved() && !discardUnsaved {
			res.Kept = append(res.Kept, l)
			continue
		}
		if err := c.remove(ctx, l); err != nil {
			res.Failed = append(res.Failed, CleanupFailure{Leftover: l, Err: err})
			continue
		}
		res.Removed = append(res.Removed, l)
	}
	return res
}

func (c Cleaner) remove(ctx context.Context, l Leftover) error {
	if !l.Gone {
		if err := c.Workmux.Remove(ctx, c.Cwd, l.Handle); err != nil {
			c.Git.RemoveWorktree(ctx, c.Cwd, l.WorktreePath)
			if l.Branch != "" {
				_ = c.Git.DeleteBranch(ctx, c.Cwd, l.Branch)
			}
		}
		if _, err := os.Stat(l.WorktreePath); err == nil {
			return fmt.Errorf("worktree %s could not be removed", l.WorktreePath)
		}
	}
	for _, p := range []string{SystemPromptPath(c.Cwd, l.RunID), PromptPath(c.Cwd, l.RunID), WorkmuxConfigPath(c.Cwd, l.RunID)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	s := l.Session
	s.Status = Removed
	return Save(c.Cwd, s)
}
