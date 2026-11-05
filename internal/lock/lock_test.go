package lock

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const deadPID = 999999

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeLock(t *testing.T, resource string, info Info) {
	t.Helper()
	if err := os.Mkdir(Dir(resource), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(info)
	if err := os.WriteFile(filepath.Join(Dir(resource), "info.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func acquire(t *testing.T, resource, agent string, timeout time.Duration) *Lock {
	t.Helper()
	l, err := Acquire(context.Background(), resource, agent, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestBreakStale(t *testing.T) {
	t.Run("leaves a lock held by a live process", func(t *testing.T) {
		resource := filepath.Join(t.TempDir(), "my-agent")
		l := acquire(t, resource, "holder", time.Second)
		defer l.Release()
		if BreakStale(resource) || !exists(Dir(resource)) {
			t.Error("live lock was broken")
		}
	})
	t.Run("leaves a lock older than 30 minutes held by a live process", func(t *testing.T) {
		resource := filepath.Join(t.TempDir(), "my-agent")
		writeLock(t, resource, Info{PID: os.Getpid(), Timestamp: time.Now().Add(-31 * time.Minute).UnixMilli(), Agent: "old"})
		if BreakStale(resource) || !exists(Dir(resource)) {
			t.Error("old live lock was broken")
		}
	})
	t.Run("leaves nothing but the lock directory behind", func(t *testing.T) {
		dir := t.TempDir()
		resource := filepath.Join(dir, "my-agent")
		writeLock(t, resource, Info{PID: deadPID, Timestamp: time.Now().UnixMilli(), Agent: "dead"})
		BreakStale(resource)
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("left behind %v", entries)
		}
	})
	t.Run("breaks a lock held by a dead process", func(t *testing.T) {
		resource := filepath.Join(t.TempDir(), "my-agent")
		writeLock(t, resource, Info{PID: deadPID, Timestamp: time.Now().UnixMilli(), Agent: "dead"})
		if !BreakStale(resource) || exists(Dir(resource)) {
			t.Error("dead lock was kept")
		}
	})
	t.Run("breaks a lock directory without info older than the stale timeout", func(t *testing.T) {
		resource := filepath.Join(t.TempDir(), "my-agent")
		_ = os.Mkdir(Dir(resource), 0o755)
		if BreakStale(resource) {
			t.Error("fresh lock without info was broken")
		}
		old := time.Now().Add(-StaleAfter - time.Minute)
		_ = os.Chtimes(Dir(resource), old, old)
		if !BreakStale(resource) {
			t.Error("old lock without info was kept")
		}
	})
	t.Run("returns false when there is no lock", func(t *testing.T) {
		if BreakStale(filepath.Join(t.TempDir(), "missing")) {
			t.Error("reported a broken lock")
		}
	})
}

func TestAcquire(t *testing.T) {
	t.Run("acquires a lock and creates the lock directory", func(t *testing.T) {
		resource := filepath.Join(t.TempDir(), "my-agent")
		l := acquire(t, resource, "test-agent", time.Second)
		defer l.Release()
		if !exists(Dir(resource)) {
			t.Error("lock directory missing")
		}
	})
	t.Run("releases the lock by removing the directory", func(t *testing.T) {
		resource := filepath.Join(t.TempDir(), "my-agent")
		acquire(t, resource, "test-agent", time.Second).Release()
		if exists(Dir(resource)) {
			t.Error("lock directory left behind")
		}
	})
	t.Run("release leaves a lock now owned by another process", func(t *testing.T) {
		resource := filepath.Join(t.TempDir(), "my-agent")
		l := acquire(t, resource, "first", time.Second)
		_ = os.RemoveAll(Dir(resource))
		writeLock(t, resource, Info{PID: os.Getppid(), Timestamp: time.Now().UnixMilli(), Agent: "second"})
		l.Release()
		if !exists(Dir(resource)) {
			t.Error("release removed another process's lock")
		}
	})
	t.Run("writes lock info with PID and agent name", func(t *testing.T) {
		resource := filepath.Join(t.TempDir(), "my-agent")
		before := time.Now().UnixMilli()
		l := acquire(t, resource, "improver", time.Second)
		defer l.Release()
		info, ok := ReadLive(Dir(resource))
		if !ok || info.PID != os.Getpid() || info.Agent != "improver" || info.Timestamp < before {
			t.Errorf("info = %+v ok=%v", info, ok)
		}
	})
	t.Run("times out when lock is held by a live process", func(t *testing.T) {
		resource := filepath.Join(t.TempDir(), "my-agent")
		l := acquire(t, resource, "holder", time.Second)
		defer l.Release()
		_, err := Acquire(context.Background(), resource, "waiter", time.Second)
		if err == nil || !strings.Contains(err.Error(), "timeout") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("stops waiting when the context is canceled", func(t *testing.T) {
		resource := filepath.Join(t.TempDir(), "my-agent")
		l := acquire(t, resource, "holder", time.Second)
		defer l.Release()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Acquire(ctx, resource, "waiter", time.Minute); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("breaks stale lock from dead process and acquires", func(t *testing.T) {
		resource := filepath.Join(t.TempDir(), "my-agent")
		writeLock(t, resource, Info{PID: deadPID, Timestamp: time.Now().UnixMilli(), Agent: "dead"})
		l := acquire(t, resource, "new-agent", 5*time.Second)
		defer l.Release()
		if info, _ := ReadLive(Dir(resource)); info.Agent != "new-agent" {
			t.Errorf("info = %+v", info)
		}
	})
	t.Run("waits on a lock older than 30 minutes while its holder is alive", func(t *testing.T) {
		resource := filepath.Join(t.TempDir(), "my-agent")
		writeLock(t, resource, Info{PID: os.Getpid(), Timestamp: time.Now().Add(-31 * time.Minute).UnixMilli(), Agent: "old"})
		_, err := Acquire(context.Background(), resource, "waiter", time.Second)
		if err == nil || !strings.Contains(err.Error(), "timeout") {
			t.Errorf("err = %v", err)
		}
		if info, _ := ReadLive(Dir(resource)); info.Agent != "old" {
			t.Errorf("info = %+v", info)
		}
	})
}

func TestBreak(t *testing.T) {
	resource := filepath.Join(t.TempDir(), "my-agent")
	_ = os.Mkdir(Dir(resource), 0o755)
	if !Break(resource) || exists(Dir(resource)) {
		t.Error("existing lock not removed")
	}
	if Break(resource) {
		t.Error("reported removing a missing lock")
	}
}

func TestReadLive(t *testing.T) {
	resource := filepath.Join(t.TempDir(), "x")
	writeLock(t, resource, Info{PID: deadPID, Agent: "dead"})
	if _, ok := ReadLive(Dir(resource)); ok {
		t.Error("dead holder reported live")
	}
	if _, ok := ReadLive(filepath.Join(t.TempDir(), "missing.lock")); ok {
		t.Error("missing lock reported live")
	}
}

func TestIsProcessAlive(t *testing.T) {
	if !IsProcessAlive(os.Getpid()) {
		t.Error("own process reported dead")
	}
	if IsProcessAlive(deadPID) || IsProcessAlive(0) {
		t.Error("dead pid reported alive")
	}
}
