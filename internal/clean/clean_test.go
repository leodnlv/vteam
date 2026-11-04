package clean

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/lock"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/runstate"
	"github.com/leodnlv/vteam/internal/tasks"
	"github.com/leodnlv/vteam/internal/tasks/taskstest"
)

func writeLock(t *testing.T, cwd, name string, pid int) {
	dir := filepath.Join(runstate.LocksDir(cwd), name+".lock")
	_ = os.MkdirAll(dir, 0o755)
	data, _ := json.Marshal(lock.Info{PID: pid, Timestamp: time.Now().UnixMilli(), Agent: name})
	_ = os.WriteFile(filepath.Join(dir, "info.json"), data, 0o644)
}

type cleanupCall struct {
	dir   string
	inUse []string
}

func TestRun(t *testing.T) {
	cwd := t.TempDir()
	_ = os.MkdirAll(filepath.Join(cwd, "vteam"), 0o755)
	_ = os.WriteFile(filepath.Join(cwd, "vteam", "vteam.config.json"), []byte(`{"worktreeDir":".wt","bogus":1}`), 0o644)
	writeLock(t, cwd, "coder", os.Getpid())
	writeLock(t, cwd, "improver", 999999)
	_ = runstate.Write(cwd, model.RunState{RunID: "20200101-000000-coder", Agent: "coder", StartedAt: "2020-01-01T00:00:00Z", WorktreePath: "/wt/busy"})

	pid := strconv.Itoa(999999)
	fake := taskstest.New(
		model.Task{Ref: "A", Title: "Dead claim", Status: "active", Metadata: map[string]string{"claimed-pid": pid, "claimed-host": "box"}},
		model.Task{Ref: "B", Title: "Other host", Status: "active", Metadata: map[string]string{"claimed-pid": pid, "claimed-host": "far"}},
		model.Task{Ref: "C", Title: "No claim", Status: "active"},
	)
	var calls []cleanupCall
	var out bytes.Buffer
	Run(context.Background(), Deps{
		Cwd: cwd, Out: &out, Host: "box", IsAlive: lock.IsProcessAlive,
		Cleanup: func(_ context.Context, _, dir string, inUse []string) ([]string, error) {
			calls = append(calls, cleanupCall{dir, inUse})
			return []string{"/wt/idle"}, nil
		},
		Tasks: func() (tasks.Manager, error) { return fake, nil },
	})

	if diff := cmp.Diff([]cleanupCall{{".wt", []string{"/wt/busy"}}}, calls, cmp.AllowUnexported(cleanupCall{})); diff != "" {
		t.Error(diff)
	}
	want := `=== vteam clean ===

Kept 1 worktree(s) in use by a running agent.
Removed 1 worktree(s):
  /wt/idle
Broke stale lock: improver.lock
Returned 1 orphaned task(s) to todo:
  A Dead claim
Left active (claimed on another host):
  B Other host
Left active (no vteam claim, reset manually if stuck):
  C No claim

Done.
`
	if diff := cmp.Diff(want, out.String()); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if fake.Task("A").Status != "todo" {
		t.Error("orphaned task not released")
	}
	if _, err := os.Stat(filepath.Join(runstate.LocksDir(cwd), "coder.lock")); err != nil {
		t.Error("live lock was broken")
	}
}

func TestRunOnAQuietProject(t *testing.T) {
	cwd := t.TempDir()
	_ = os.MkdirAll(runstate.LocksDir(cwd), 0o755)
	var out bytes.Buffer
	Run(context.Background(), Deps{
		Cwd: cwd, Out: &out, IsAlive: lock.IsProcessAlive,
		Cleanup: func(_ context.Context, _, dir string, _ []string) ([]string, error) {
			if dir != ".vteam-worktrees" {
				t.Errorf("default worktree dir = %q", dir)
			}
			return nil, nil
		},
		Tasks: func() (tasks.Manager, error) { return taskstest.New(), nil },
	})
	want := "=== vteam clean ===\n\nNo orphaned worktrees found.\nNo stale locks found.\nNo orphaned active tasks found.\n\nDone.\n"
	if diff := cmp.Diff(want, out.String()); diff != "" {
		t.Error(diff)
	}
}

func TestRunWithoutGitOrConfig(t *testing.T) {
	var out bytes.Buffer
	Run(context.Background(), Deps{
		Cwd: t.TempDir(), Out: &out, IsAlive: lock.IsProcessAlive,
		Cleanup: func(context.Context, string, string, []string) ([]string, error) { return nil, errors.New("not a git repo") },
		Tasks:   func() (tasks.Manager, error) { return nil, errors.New("vteam.config.json not found") },
	})
	want := "=== vteam clean ===\n\nNo worktrees to clean (or not a git repo).\nSkipped task cleanup: vteam.config.json not found\n\nDone.\n"
	if diff := cmp.Diff(want, out.String()); diff != "" {
		t.Error(diff)
	}
}
