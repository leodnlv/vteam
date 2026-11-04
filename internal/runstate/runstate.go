// Package runstate persists per-run state under vteam/.runs and finds the runs that are live right now.
package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leodnlv/vteam/internal/lock"
	"github.com/leodnlv/vteam/internal/model"
)

// transientLocks are held only for a moment and never mark a running job.
var transientLocks = map[string]bool{"task-claim": true}

func RunsDir(cwd string) string  { return filepath.Join(cwd, "vteam", ".runs") }
func LocksDir(cwd string) string { return filepath.Join(cwd, "vteam", ".locks") }

func Write(cwd string, state model.RunState) error {
	dir := RunsDir(cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, state.RunID+".json"), data, 0o644)
}

type LiveRun struct {
	Name  string
	Lock  lock.Info
	State *model.RunState
}

// FindLive turns live agent locks into running jobs, each with its newest run state started before the lock.
func FindLive(cwd string) []LiveRun {
	entries, err := os.ReadDir(LocksDir(cwd))
	if err != nil {
		return nil
	}
	var runs []LiveRun
	for _, e := range entries {
		name, isLock := strings.CutSuffix(e.Name(), ".lock")
		if !isLock || transientLocks[name] {
			continue
		}
		info, ok := lock.ReadLive(filepath.Join(LocksDir(cwd), e.Name()))
		if !ok {
			continue
		}
		runs = append(runs, LiveRun{Name: name, Lock: info, State: FindLatest(cwd, name, info.Time())})
	}
	return runs
}

func LiveWorktreePaths(runs []LiveRun) []string {
	var paths []string
	for _, r := range runs {
		if r.State != nil && r.State.WorktreePath != "" {
			paths = append(paths, r.State.WorktreePath)
		}
	}
	return paths
}

func FindLatest(cwd, agent string, startedBefore time.Time) *model.RunState {
	entries, err := os.ReadDir(RunsDir(cwd))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-"+agent+".json") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	slices.Reverse(names)
	for _, name := range names {
		state, ok := read(filepath.Join(RunsDir(cwd), name))
		if !ok || state.Agent != agent {
			continue
		}
		if started, err := time.Parse(time.RFC3339Nano, state.StartedAt); err == nil && !started.After(startedBefore) {
			return &state
		}
	}
	return nil
}

func read(path string) (model.RunState, bool) {
	var state model.RunState
	data, err := os.ReadFile(path)
	if err != nil {
		return state, false
	}
	return state, json.Unmarshal(data, &state) == nil
}
