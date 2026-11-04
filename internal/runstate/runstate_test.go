package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/lock"
	"github.com/leodnlv/vteam/internal/model"
)

func write(t *testing.T, cwd string, s model.RunState) {
	t.Helper()
	if err := Write(cwd, s); err != nil {
		t.Fatal(err)
	}
}

func TestFindLatest(t *testing.T) {
	t.Run("returns nil without any runs", func(t *testing.T) {
		if s := FindLatest(t.TempDir(), "coder", time.Now()); s != nil {
			t.Errorf("got %+v", s)
		}
	})

	t.Run("returns the newest run of the agent started before the given time", func(t *testing.T) {
		cwd := t.TempDir()
		write(t, cwd, model.RunState{RunID: "20260101-100000-coder", Agent: "coder", Status: model.RunCompleted, StartedAt: "2026-01-01T10:00:00Z"})
		write(t, cwd, model.RunState{RunID: "20260101-110000-coder", Agent: "coder", Status: model.RunClaudeRunning, StartedAt: "2026-01-01T11:00:00Z"})
		write(t, cwd, model.RunState{RunID: "20260101-120000-coder", Agent: "coder", Status: model.RunStarted, StartedAt: "2026-01-01T12:00:00Z"})
		write(t, cwd, model.RunState{RunID: "20260101-113000-improver", Agent: "improver", Status: model.RunStarted, StartedAt: "2026-01-01T11:30:00Z"})
		s := FindLatest(cwd, "coder", time.Date(2026, 1, 1, 11, 30, 0, 0, time.UTC))
		if s == nil || s.RunID != "20260101-110000-coder" {
			t.Errorf("got %+v", s)
		}
	})

	t.Run("does not confuse agents whose names end the same way", func(t *testing.T) {
		cwd := t.TempDir()
		write(t, cwd, model.RunState{RunID: "20260101-100000-test-coder", Agent: "test-coder", Status: model.RunStarted, StartedAt: "2026-01-01T10:00:00Z"})
		if s := FindLatest(cwd, "coder", time.Now()); s != nil {
			t.Errorf("got %+v", s)
		}
	})

	t.Run("skips unreadable state files", func(t *testing.T) {
		cwd := t.TempDir()
		write(t, cwd, model.RunState{RunID: "20260101-100000-coder", Agent: "coder", StartedAt: "2026-01-01T10:00:00Z"})
		_ = os.WriteFile(filepath.Join(RunsDir(cwd), "20260101-110000-coder.json"), []byte("{broken"), 0o644)
		if s := FindLatest(cwd, "coder", time.Now()); s == nil || s.RunID != "20260101-100000-coder" {
			t.Errorf("got %+v", s)
		}
	})
}

func writeLock(t *testing.T, cwd, name string, info lock.Info) {
	t.Helper()
	dir := filepath.Join(LocksDir(cwd), name+".lock")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(info)
	_ = os.WriteFile(filepath.Join(dir, "info.json"), data, 0o644)
}

func TestFindLive(t *testing.T) {
	cwd := t.TempDir()
	now := time.Now()
	writeLock(t, cwd, "coder", lock.Info{PID: os.Getpid(), Timestamp: now.UnixMilli(), Agent: "coder"})
	writeLock(t, cwd, "improver", lock.Info{PID: 999999, Timestamp: now.UnixMilli(), Agent: "improver"})
	writeLock(t, cwd, "task-claim", lock.Info{PID: os.Getpid(), Timestamp: now.UnixMilli(), Agent: "coder"})
	_ = os.WriteFile(filepath.Join(LocksDir(cwd), "cron.pid"), []byte("1"), 0o644)
	write(t, cwd, model.RunState{RunID: "20260101-100000-coder", Agent: "coder", StartedAt: now.Add(-time.Second).UTC().Format(time.RFC3339Nano), WorktreePath: "/wt/a"})
	write(t, cwd, model.RunState{RunID: "29990101-100000-coder", Agent: "coder", StartedAt: now.Add(time.Hour).UTC().Format(time.RFC3339Nano), WorktreePath: "/wt/later"})

	runs := FindLive(cwd)
	if len(runs) != 1 || runs[0].Name != "coder" || runs[0].State == nil {
		t.Fatalf("runs = %+v", runs)
	}
	if diff := cmp.Diff([]string{"/wt/a"}, LiveWorktreePaths(runs)); diff != "" {
		t.Error(diff)
	}
	if FindLive(t.TempDir()) != nil {
		t.Error("runs found without a locks directory")
	}
}
