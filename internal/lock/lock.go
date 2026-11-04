// Package lock implements advisory file locks as directories created with an atomic mkdir.
package lock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const StaleAfter = 30 * time.Minute

type Info struct {
	PID       int    `json:"pid"`
	Timestamp int64  `json:"timestamp"`
	Agent     string `json:"agent"`
}

func (i Info) Time() time.Time { return time.UnixMilli(i.Timestamp) }

type Lock struct {
	Path string
}

func (l *Lock) Release() {
	if l != nil {
		_ = os.RemoveAll(l.Path)
	}
}

func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func Dir(resource string) string { return resource + ".lock" }

func readInfo(lockDir string) (Info, error) {
	var info Info
	data, err := os.ReadFile(filepath.Join(lockDir, "info.json"))
	if err != nil {
		return info, err
	}
	err = json.Unmarshal(data, &info)
	return info, err
}

// ReadLive returns the lock's info when its holder is still alive.
func ReadLive(lockDir string) (Info, bool) {
	info, err := readInfo(lockDir)
	if err != nil || !IsProcessAlive(info.PID) {
		return Info{}, false
	}
	return info, true
}

func isStale(lockDir string) bool {
	info, err := readInfo(lockDir)
	if errors.Is(err, os.ErrNotExist) {
		stat, statErr := os.Stat(lockDir)
		return statErr != nil || time.Since(stat.ModTime()) > StaleAfter
	}
	if err != nil || !IsProcessAlive(info.PID) {
		return true
	}
	return time.Since(info.Time()) > StaleAfter
}

func Acquire(ctx context.Context, resource, agent string, timeout time.Duration) (*Lock, error) {
	dir := Dir(resource)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			info, _ := json.Marshal(Info{PID: os.Getpid(), Timestamp: time.Now().UnixMilli(), Agent: agent})
			if err := os.WriteFile(filepath.Join(dir, "info.json"), info, 0o644); err != nil {
				_ = os.RemoveAll(dir)
				return nil, err
			}
			return &Lock{Path: dir}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if isStale(dir) {
			_ = os.RemoveAll(dir)
			continue
		}
		wait := 500*time.Millisecond + rand.N(500*time.Millisecond)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, fmt.Errorf("timeout acquiring lock on %s", resource)
}

// BreakStale removes the lock when its holder is dead or it is older than StaleAfter.
func BreakStale(resource string) bool {
	dir := Dir(resource)
	if _, err := os.Stat(dir); err != nil || !isStale(dir) {
		return false
	}
	_ = os.RemoveAll(dir)
	return true
}

func Break(resource string) bool {
	dir := Dir(resource)
	if _, err := os.Stat(dir); err != nil {
		return false
	}
	_ = os.RemoveAll(dir)
	return true
}
