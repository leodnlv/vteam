// Package workmux drives the workmux CLI, which pairs git worktrees with tmux windows, for interactive runs.
package workmux

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/leodnlv/vteam/internal/execx"
)

type Worktree struct {
	Handle string `json:"handle"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	IsOpen bool   `json:"is_open"`
}

type OpenOptions struct {
	Handle string
	// PromptFile is injected into the agent pane as its first prompt.
	PromptFile string
	// ConfigFile is an extra workmux config, used to set the agent command.
	ConfigFile string
	// ParentSession is the tmux session the window is created in. A window in the caller's own session
	// would be switched to, so interactive runs always use a dedicated one.
	ParentSession string
}

// Client is the workmux work an interactive run does; *CLI implements it.
type Client interface {
	Add(ctx context.Context, repoRoot, branch string) (Worktree, error)
	Open(ctx context.Context, repoRoot string, o OpenOptions) error
	Remove(ctx context.Context, repoRoot, handle string) error
	List(ctx context.Context, repoRoot string) ([]Worktree, error)
}

type CLI struct {
	Exec execx.Runner
}

func New() *CLI { return &CLI{Exec: execx.OS{}} }

func (c *CLI) run(ctx context.Context, dir string, args ...string) (string, error) {
	return c.Exec.Run(ctx, execx.Cmd{Name: "workmux", Args: args, Dir: dir})
}

// Add creates a worktree for an existing local branch without a tmux window.
func (c *CLI) Add(ctx context.Context, repoRoot, branch string) (Worktree, error) {
	out, err := c.run(ctx, repoRoot, "add", branch, "--headless", "--json")
	if err != nil {
		return Worktree{}, err
	}
	var receipt struct {
		Handle string `json:"handle"`
		Branch string `json:"branch"`
		Path   string `json:"worktree_path"`
	}
	if err := json.Unmarshal([]byte(out), &receipt); err != nil {
		return Worktree{}, fmt.Errorf("unexpected workmux add output: %w", err)
	}
	if receipt.Handle == "" || receipt.Path == "" {
		return Worktree{}, fmt.Errorf("unexpected workmux add output: %s", out)
	}
	return Worktree{Handle: receipt.Handle, Branch: receipt.Branch, Path: receipt.Path}, nil
}

func (c *CLI) Open(ctx context.Context, repoRoot string, o OpenOptions) error {
	args := []string{"open", o.Handle}
	if o.PromptFile != "" {
		args = append(args, "--prompt-file", o.PromptFile)
	}
	if o.ConfigFile != "" {
		args = append(args, "--config", o.ConfigFile)
	}
	if o.ParentSession != "" {
		args = append(args, "--parent-session", o.ParentSession)
	}
	_, err := c.run(ctx, repoRoot, args...)
	return err
}

// Remove deletes the worktree, its local branch, and its window, discarding uncommitted changes.
func (c *CLI) Remove(ctx context.Context, repoRoot, handle string) error {
	_, err := c.run(ctx, repoRoot, "remove", "--force", handle)
	return err
}

func (c *CLI) List(ctx context.Context, repoRoot string) ([]Worktree, error) {
	out, err := c.run(ctx, repoRoot, "list", "--json")
	if err != nil {
		return nil, err
	}
	var list []Worktree
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, fmt.Errorf("unexpected workmux list output: %w", err)
	}
	return list, nil
}

// AgentConfig is a workmux config file that sets the agent command.
func AgentConfig(agentCommand string) string {
	quoted, _ := json.Marshal(agentCommand)
	return "agent: " + string(quoted) + "\n"
}
