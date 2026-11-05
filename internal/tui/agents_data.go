package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/ensuremerge"
	"github.com/leodnlv/vteam/internal/lock"
	"github.com/leodnlv/vteam/internal/runlog"
	"github.com/leodnlv/vteam/internal/runstate"
	"github.com/leodnlv/vteam/internal/schedule"
)

type Agent struct {
	Name      string
	Config    *config.Agent
	LoadError string
	// Builtin marks the merge ensurer, which has a schedule and logs but no agent directory.
	Builtin bool
	Running *lock.Info
	// Logs are run log paths, oldest first.
	Logs    []string
	NextRun time.Time
}

// LoadAgents lists every agent, the merge ensurer, and any other name with a log directory.
func LoadAgents(cwd string, now time.Time) []Agent {
	cfg, _ := config.Load(cwd)
	live := map[string]lock.Info{}
	for _, run := range runstate.FindLive(cwd) {
		live[run.Name] = run.Lock
	}

	names := map[string]bool{ensuremerge.Name: true}
	for _, name := range config.ListAgentNames(cwd) {
		names[name] = true
	}
	if entries, err := os.ReadDir(filepath.Join(cwd, "vteam", ".logs")); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				names[e.Name()] = true
			}
		}
	}

	agents := make([]Agent, 0, len(names))
	for name := range names {
		a := Agent{Name: name, Logs: listLogs(cwd, name)}
		if info, ok := live[name]; ok {
			a.Running = &info
		}
		if name == ensuremerge.Name {
			a.Builtin = true
			if cfg != nil && cfg.MergeEnsurer.Cron != "" {
				a.NextRun = schedule.NextRun(cfg.MergeEnsurer.Cron, now)
			}
		} else if resolved, err := config.ResolveAgent(name, cwd); err != nil {
			a.LoadError = err.Error()
		} else {
			a.Config = resolved
			if resolved.Cron != "" {
				a.NextRun = schedule.NextRun(resolved.Cron, now)
			}
		}
		agents = append(agents, a)
	}
	slices.SortFunc(agents, func(a, b Agent) int {
		if a.Builtin != b.Builtin {
			if a.Builtin {
				return 1
			}
			return -1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return agents
}

func listLogs(cwd, agent string) []string {
	entries, err := os.ReadDir(runlog.AgentDir(cwd, agent))
	if err != nil {
		return nil
	}
	var logs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
			logs = append(logs, filepath.Join(runlog.AgentDir(cwd, agent), e.Name()))
		}
	}
	slices.Sort(logs)
	return logs
}

var logStamp = regexp.MustCompile(`^(\d{4}-\d\d-\d\d)T(\d\d)-(\d\d)-(\d\d)-(\d{3})Z$`)

// logStartedAt parses the start time from a run log filename (see runlog.FormatFilename).
func logStartedAt(path string) (time.Time, bool) {
	m := logStamp.FindStringSubmatch(strings.TrimSuffix(filepath.Base(path), ".log"))
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, m[1]+"T"+m[2]+":"+m[3]+":"+m[4]+"."+m[5]+"Z")
	return t, err == nil
}

type logFile struct {
	path    string
	size    int64
	modTime time.Time
	lines   []string
}

// readLog returns prev unchanged when the file has the same size and mtime.
func readLog(path string, prev logFile) logFile {
	if path == "" {
		return logFile{}
	}
	info, err := os.Stat(path)
	if err != nil {
		return logFile{path: path}
	}
	if prev.path == path && prev.size == info.Size() && prev.modTime.Equal(info.ModTime()) {
		return prev
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return logFile{path: path}
	}
	text := strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var lines []string
	if text != "" {
		lines = strings.Split(text, "\n")
	}
	return logFile{path: path, size: info.Size(), modTime: info.ModTime(), lines: lines}
}
