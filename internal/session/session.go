// Package session persists interactive runs under vteam/.sessions: the state a run hands off to the
// interactive Claude session, and what `vteam submit` needs to settle it.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/leodnlv/vteam/internal/model"
)

type Status string

const (
	Open       Status = "open"
	Submitting Status = "submitting"
	Submitted  Status = "submitted"
	Abandoned  Status = "abandoned"
)

type Session struct {
	RunID        string `json:"runId"`
	Agent        string `json:"agent"`
	Status       Status `json:"status"`
	StartedAt    string `json:"startedAt"`
	Handle       string `json:"handle"`
	WorktreePath string `json:"worktreePath"`
	// Branch is the local branch; PushBranch the remote branch it is pushed to (the PR branch on follow-ups).
	Branch     string `json:"branch"`
	PushBranch string `json:"pushBranch"`
	// RemoteSHA is PushBranch's commit on origin at hand-off, "" for a new branch; the push lease.
	RemoteSHA string `json:"remoteSha,omitempty"`
	// StartSHA is HEAD at hand-off; commits past it are published even without a commit message.
	StartSHA string                   `json:"startSha"`
	Task     *model.Task              `json:"task,omitempty"`
	Review   *model.PRReviewContext   `json:"review,omitempty"`
	FollowUp *model.PRFollowUpContext `json:"followUp,omitempty"`
	Focus    string                   `json:"focus,omitempty"`
	LogPath  string                   `json:"logPath"`
	// Scratch marks a worktree made only to host the session for an agent without worktree: true.
	Scratch bool `json:"scratch,omitempty"`
	// InUserSession means the window was opened in the user's own tmux session, so closing it never ends the
	// session; a window in the dedicated vteam session (vteam ran outside tmux) is left for the user to close.
	InUserSession bool `json:"inUserSession,omitempty"`
	// Outcome and MemoryUpdate are set by submit for the detached finalize step.
	Outcome      *model.RunOutcome `json:"outcome,omitempty"`
	MemoryUpdate string            `json:"memoryUpdate,omitempty"`
}

func Root(cwd string) string { return filepath.Join(cwd, "vteam", ".sessions") }

func Dir(cwd, runID string) string { return filepath.Join(Root(cwd), runID) }

func path(cwd, runID string) string { return filepath.Join(Dir(cwd, runID), "session.json") }

func SystemPromptPath(cwd, runID string) string {
	return filepath.Join(Dir(cwd, runID), "system-prompt.md")
}

func PromptPath(cwd, runID string) string { return filepath.Join(Dir(cwd, runID), "prompt.md") }

func WorkmuxConfigPath(cwd, runID string) string {
	return filepath.Join(Dir(cwd, runID), "workmux.yaml")
}

// EnsureDir creates the session's directory, and vteam/.sessions with a .gitignore of its own.
func EnsureDir(cwd, runID string) error {
	if err := os.MkdirAll(Dir(cwd, runID), 0o755); err != nil {
		return err
	}
	ignore := filepath.Join(Root(cwd), ".gitignore")
	if _, err := os.Stat(ignore); err == nil {
		return nil
	}
	return os.WriteFile(ignore, []byte("*\n"), 0o644)
}

func Save(cwd string, s Session) error {
	if err := EnsureDir(cwd, s.RunID); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path(cwd, s.RunID) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path(cwd, s.RunID))
}

var ErrNotFound = errors.New("no interactive session found")

func Load(cwd, runID string) (Session, error) {
	data, err := os.ReadFile(path(cwd, runID))
	if errors.Is(err, os.ErrNotExist) {
		return Session{}, fmt.Errorf("%w with id %s", ErrNotFound, runID)
	}
	if err != nil {
		return Session{}, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return Session{}, fmt.Errorf("session %s: %w", runID, err)
	}
	return s, nil
}

// List returns every readable session, oldest first.
func List(cwd string) []Session {
	entries, err := os.ReadDir(Root(cwd))
	if err != nil {
		return nil
	}
	var sessions []Session
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if s, err := Load(cwd, e.Name()); err == nil {
			sessions = append(sessions, s)
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].StartedAt < sessions[j].StartedAt })
	return sessions
}

func ListOpen(cwd string) []Session {
	var open []Session
	for _, s := range List(cwd) {
		if s.Status == Open || s.Status == Submitting {
			open = append(open, s)
		}
	}
	return open
}

// WorktreePaths are the worktrees of sessions not yet submitted, which `vteam clean` must leave alone.
func WorktreePaths(cwd string) []string {
	var paths []string
	for _, s := range List(cwd) {
		if s.Status != Submitted {
			paths = append(paths, s.WorktreePath)
		}
	}
	return paths
}

func canonical(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

// FindByDir returns the session whose worktree contains dir.
func FindByDir(cwd, dir string) (Session, error) {
	dir = canonical(dir)
	for _, s := range List(cwd) {
		root := canonical(s.WorktreePath)
		if dir == root || strings.HasPrefix(dir, root+string(filepath.Separator)) {
			return s, nil
		}
	}
	return Session{}, fmt.Errorf("%w for %s", ErrNotFound, dir)
}

// ProjectDir finds the vteam project for dir: dir itself when it holds vteam/.sessions, otherwise the main
// checkout of the git repository dir is a worktree of.
func ProjectDir(ctx context.Context, dir string) (string, error) {
	if _, err := os.Stat(Root(dir)); err == nil {
		return dir, nil
	}
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", fmt.Errorf("%s is not a vteam project or a worktree of one", dir)
	}
	main := filepath.Dir(strings.TrimSpace(string(out)))
	if _, err := os.Stat(filepath.Join(main, "vteam")); err != nil {
		return "", fmt.Errorf("%s is not a vteam project or a worktree of one", dir)
	}
	return main, nil
}
