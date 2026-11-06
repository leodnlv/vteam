package tui

import (
	"github.com/leodnlv/vteam/internal/session"
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
	Logs []string
	// Cron is the enabled cron pattern; "" when the agent's cron is off.
	Cron string
	// DefaultCron is the pattern from agent.json, or mergeEnsurer.cron for the merge ensurer.
	DefaultCron string
	// NextRun is the next fire time of the enabled cron.
	NextRun time.Time
	// Sessions are the handles of the agent's open interactive sessions.
	Sessions []string
}

// Runnable reports whether the agent can be started: it loads and is an agent or the merge ensurer.
func (a Agent) Runnable() bool {
	return a.LoadError == "" && (a.Config != nil || a.Builtin)
}

// LoadAgents lists every agent, the merge ensurer, and any other name with a log directory.
func LoadAgents(cwd string, now time.Time) []Agent {
	cfg, _ := config.Load(cwd)
	enabled, _ := schedule.Enabled(cwd)
	sessions := map[string][]string{}
	for _, s := range session.ListOpen(cwd) {
		sessions[s.Agent] = append(sessions[s.Agent], s.Handle)
	}
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
		a := Agent{Name: name, Logs: listLogs(cwd, name), Cron: enabled[name], Sessions: sessions[name]}
		if info, ok := live[name]; ok {
			a.Running = &info
		}
		if name == ensuremerge.Name {
			a.Builtin = true
			if cfg != nil {
				a.DefaultCron = cfg.MergeEnsurer.Cron
			}
		} else if resolved, err := config.ResolveAgent(name, cwd); err != nil {
			a.LoadError = err.Error()
		} else {
			a.Config = resolved
			a.DefaultCron = resolved.Cron
		}
		if a.Cron != "" {
			a.NextRun = schedule.NextRun(a.Cron, now)
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
