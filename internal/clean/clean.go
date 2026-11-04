// Package clean removes worktrees no running agent uses, breaks stale locks, and releases orphaned active tasks.
package clean

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/lock"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/runstate"
	"github.com/leodnlv/vteam/internal/tasks"
)

const defaultWorktreeDir = ".vteam-worktrees"

type Deps struct {
	Cwd string
	Out io.Writer
	// Cleanup removes worktrees under worktreeDir that no path in inUse refers to, and returns them.
	Cleanup func(ctx context.Context, repoRoot, worktreeDir string, inUse []string) ([]string, error)
	// Tasks returns the task manager, or an error when the config cannot be loaded.
	Tasks   func() (tasks.Manager, error)
	IsAlive func(pid int) bool
	Host    string
}

// worktreeDir reads worktreeDir leniently, so clean still works with a config that fails validation.
func worktreeDir(cwd string) string {
	var raw struct {
		WorktreeDir string `json:"worktreeDir"`
	}
	if data, err := os.ReadFile(config.ConfigPath(cwd)); err == nil && json.Unmarshal(data, &raw) == nil && raw.WorktreeDir != "" {
		return raw.WorktreeDir
	}
	return defaultWorktreeDir
}

func Run(ctx context.Context, d Deps) {
	p := func(format string, args ...any) { fmt.Fprintf(d.Out, format+"\n", args...) }
	p("=== vteam clean ===\n")

	inUse := runstate.LiveWorktreePaths(runstate.FindLive(d.Cwd))
	if cleaned, err := d.Cleanup(ctx, d.Cwd, worktreeDir(d.Cwd), inUse); err != nil {
		p("No worktrees to clean (or not a git repo).")
	} else {
		if len(inUse) > 0 {
			p("Kept %d worktree(s) in use by a running agent.", len(inUse))
		}
		if len(cleaned) > 0 {
			p("Removed %d worktree(s):", len(cleaned))
			for _, path := range cleaned {
				p("  %s", path)
			}
		} else {
			p("No orphaned worktrees found.")
		}
	}

	breakStaleLocks(d.Cwd, p)
	releaseOrphanedTasks(ctx, d, p)
	p("\nDone.")
}

func breakStaleLocks(cwd string, p func(string, ...any)) {
	entries, err := os.ReadDir(runstate.LocksDir(cwd))
	if err != nil {
		return
	}
	broken := 0
	for _, e := range entries {
		name, isLock := strings.CutSuffix(e.Name(), ".lock")
		if isLock && lock.BreakStale(filepath.Join(runstate.LocksDir(cwd), name)) {
			p("Broke stale lock: %s", e.Name())
			broken++
		}
	}
	if broken == 0 {
		p("No stale locks found.")
	}
}

func releaseOrphanedTasks(ctx context.Context, d Deps, p func(string, ...any)) {
	manager, err := d.Tasks()
	var sweep tasks.OrphanSweep
	if err == nil {
		sweep, err = tasks.ReleaseOrphaned(ctx, manager, d.IsAlive, d.Host)
	}
	if err != nil {
		p("Skipped task cleanup: %s", err)
		return
	}
	list := func(header string, ts []model.Task) {
		if len(ts) == 0 {
			return
		}
		p("%s", header)
		for _, t := range ts {
			p("  %s %s", t.Ref, t.Title)
		}
	}
	if len(sweep.Released) == 0 {
		p("No orphaned active tasks found.")
	}
	list(fmt.Sprintf("Returned %d orphaned task(s) to todo:", len(sweep.Released)), sweep.Released)
	list("Left active (claimed on another host):", sweep.ForeignHost)
	list("Left active (no vteam claim, reset manually if stuck):", sweep.Unclaimed)
}
