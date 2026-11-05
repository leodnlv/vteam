package schedule

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/cron"
	"github.com/leodnlv/vteam/internal/ensuremerge"
)

// The enabled crons are local state in vteam/.cron/schedules.json. The cron in agent.json and
// mergeEnsurer.cron in vteam.config.json are only the default patterns offered when enabling.

func StateDir(cwd string) string  { return filepath.Join(cwd, "vteam", ".cron") }
func StatePath(cwd string) string { return filepath.Join(StateDir(cwd), "schedules.json") }

// LogPath is where a scheduler started in the background (StartDetached) writes its output.
func LogPath(cwd string) string { return filepath.Join(StateDir(cwd), "scheduler.log") }

type stateFile struct {
	Jobs map[string]stateJob `json:"jobs"`
}

type stateJob struct {
	Cron string `json:"cron"`
}

// Enabled returns the cron pattern of every enabled job by name. A missing state file means none.
func Enabled(cwd string) (map[string]string, error) {
	data, err := os.ReadFile(StatePath(cwd))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var s stateFile
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", StatePath(cwd), err)
	}
	out := make(map[string]string, len(s.Jobs))
	for name, j := range s.Jobs {
		out[name] = j.Cron
	}
	return out, nil
}

// Enable turns on the cron of an agent or the merge ensurer, replacing its pattern if already on.
func Enable(cwd, name, expr string) error {
	if _, err := cron.Parse(expr); err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	if err := checkJobName(cwd, name); err != nil {
		return err
	}
	jobs, err := Enabled(cwd)
	if err != nil {
		return err
	}
	jobs[name] = expr
	return writeState(cwd, jobs)
}

func Disable(cwd, name string) error {
	jobs, err := Enabled(cwd)
	if err != nil {
		return err
	}
	if _, ok := jobs[name]; !ok {
		return fmt.Errorf("%s has no cron enabled", name)
	}
	delete(jobs, name)
	return writeState(cwd, jobs)
}

func checkJobName(cwd, name string) error {
	if name == ensuremerge.Name {
		return nil
	}
	if _, err := config.ResolveAgent(name, cwd); err != nil {
		return err
	}
	return nil
}

func writeState(cwd string, jobs map[string]string) error {
	if err := ensureStateDir(cwd); err != nil {
		return err
	}
	s := stateFile{Jobs: make(map[string]stateJob, len(jobs))}
	for name, expr := range jobs {
		s.Jobs[name] = stateJob{Cron: expr}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := StatePath(cwd) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, StatePath(cwd))
}

// ensureStateDir creates vteam/.cron with a .gitignore of its own, so projects set up before it existed don't commit it.
func ensureStateDir(cwd string) error {
	if err := os.MkdirAll(StateDir(cwd), 0o755); err != nil {
		return err
	}
	ignore := filepath.Join(StateDir(cwd), ".gitignore")
	if _, err := os.Stat(ignore); err == nil {
		return nil
	}
	return os.WriteFile(ignore, []byte("*\n"), 0o644)
}

// DefaultCron is the pattern from agent.json, or mergeEnsurer.cron for the merge ensurer; "" when there is none.
func DefaultCron(cwd, name string) string {
	if name == ensuremerge.Name {
		if cfg, err := config.Load(cwd); err == nil {
			return cfg.MergeEnsurer.Cron
		}
		return ""
	}
	if agent, err := config.ResolveAgent(name, cwd); err == nil {
		return agent.Cron
	}
	return ""
}

// SaveDefaultCron writes expr as the default pattern: agent.json for an agent, vteam.config.json for the merge ensurer.
func SaveDefaultCron(cwd, name, expr string) error {
	if name == ensuremerge.Name {
		return config.SetMergeEnsurerCron(cwd, expr)
	}
	return config.SetAgentCron(cwd, name, expr)
}

// DefaultCronFile names the file SaveDefaultCron writes for name.
func DefaultCronFile(name string) string {
	if name == ensuremerge.Name {
		return "vteam.config.json"
	}
	return "agent.json"
}

// JobCommand is the vteam subcommand that runs a job once.
func JobCommand(name string) []string {
	if name == ensuremerge.Name {
		return []string{"ensure-merge"}
	}
	return []string{"run", name}
}
