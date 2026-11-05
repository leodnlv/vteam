// Package cli wires vteam's commands to cobra.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/leodnlv/vteam/internal/ansi"
	"github.com/leodnlv/vteam/internal/clean"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/ensuremerge"
	"github.com/leodnlv/vteam/internal/git"
	"github.com/leodnlv/vteam/internal/lock"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/platform"
	"github.com/leodnlv/vteam/internal/run"
	"github.com/leodnlv/vteam/internal/runlog"
	"github.com/leodnlv/vteam/internal/runstate"
	"github.com/leodnlv/vteam/internal/scaffold"
	"github.com/leodnlv/vteam/internal/schedule"
	"github.com/leodnlv/vteam/internal/skill"
	"github.com/leodnlv/vteam/internal/status"
	"github.com/leodnlv/vteam/internal/tasks"
	"github.com/leodnlv/vteam/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Main runs the CLI with os.Args and returns the process exit code.
func Main() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	root := NewRoot()
	root.SetArgs(os.Args[1:])
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	if !errors.Is(err, log.ErrReported) {
		fmt.Fprintln(os.Stderr, ansi.Fail("error: "+err.Error()))
	}
	return 1
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "dev"
}

func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "vteam",
		Short:         "Virtual development team framework powered by Claude Code",
		Version:       version(),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE:          runTUI,
	}
	root.AddCommand(initCmd(), runCmd(), statusCmd(), cleanCmd(), ensureMergeCmd(), cronCmd(), skillCmd())
	return root
}

func cwd() (string, error) {
	return os.Getwd()
}

func requireVteamDir(dir string, errOut io.Writer) error {
	if _, err := os.Stat(filepath.Join(dir, "vteam")); err != nil {
		fmt.Fprintln(errOut, ansi.Fail("No vteam/ directory found. Run 'vteam init' first."))
		return log.ErrReported
	}
	return nil
}

func relative(base, path string) string {
	if rel, err := filepath.Rel(base, path); err == nil {
		return rel
	}
	return path
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Scaffold vteam/ directory with default agents and config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			return scaffold.Init(dir, cmd.OutOrStdout())
		},
	}
}

func runCmd() *cobra.Command {
	var focus string
	cmd := &cobra.Command{
		Use:    "run [agent]",
		Short:  "Run a specific agent (omit the agent to list available agents)",
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				listAgents(dir, cmd.OutOrStdout())
				return nil
			}
			return runAgent(cmd.Context(), dir, args[0], focus)
		},
	}
	cmd.Flags().StringVar(&focus, "focus", "", "priority context injected into the agent prompt")
	return cmd
}

func listAgents(dir string, out io.Writer) {
	names := config.ListAgentNames(dir)
	if len(names) == 0 {
		fmt.Fprintln(out, "No agents found. Run 'vteam init' first.")
		return
	}
	fmt.Fprintln(out, "Available agents:")
	fmt.Fprintln(out)
	for _, name := range names {
		agent, err := config.ResolveAgent(name, dir)
		if err != nil {
			fmt.Fprintf(out, "  %s  (invalid config)\n", name)
			continue
		}
		var flags []string
		if agent.Worktree {
			flags = append(flags, "worktree")
		}
		if agent.ReadOnly {
			flags = append(flags, "readOnly")
		}
		if agent.Output != "" {
			flags = append(flags, "output: "+agent.Output)
		}
		if agent.Input != "" {
			flags = append(flags, "input: "+agent.Input)
		}
		if agent.AutoPR {
			flags = append(flags, "autoPR")
		}
		suffix := ""
		if len(flags) > 0 {
			suffix = "  (" + strings.Join(flags, ", ") + ")"
		}
		fmt.Fprintf(out, "  %s%s\n", name, suffix)
	}
}

func runAgent(ctx context.Context, dir, name, focus string) error {
	agent, err := config.ResolveAgent(name, dir)
	if err != nil {
		return err
	}
	logger := log.Std()
	runLog, err := runlog.Start(dir, agent.Name, time.Now(), logger)
	if err != nil {
		return err
	}
	defer func() { _ = runLog.Close() }()
	logger.StartClock()
	logger.Banner(agent.Name, "log: "+relative(dir, runLog.Path))

	cfg, err := config.Load(dir)
	if err != nil {
		logger.WriteFile(err.Error() + "\n")
		return err
	}
	return run.Run(ctx, run.NewDeps(dir, cfg, logger), agent, run.Options{Focus: focus})
}

func statusDeps(dir string, cfg *config.Config) status.Deps {
	return status.Deps{
		Cwd: dir, Config: cfg, Tasks: tasks.NewAven(cfg.Tasks), Now: time.Now, Host: tasks.Hostname(),
		IsAlive: lock.IsProcessAlive, Orphans: git.New(log.Discard()).FindOrphaned,
	}
}

func renderStatus(ctx context.Context, dir string) ([]string, error) {
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}
	return status.Render(ctx, statusDeps(dir, cfg))
}

// runTUI opens the TUI in a terminal inside a vteam project, and prints help everywhere else.
func runTUI(cmd *cobra.Command, _ []string) error {
	dir, err := cwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "vteam")); err != nil {
		if helpErr := cmd.Help(); helpErr != nil {
			return helpErr
		}
		fmt.Fprintln(cmd.ErrOrStderr(), ansi.Warn("\nNo vteam/ directory found. Run 'vteam init' to set one up."))
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return cmd.Help()
	}
	return tui.Run(cmd.Context(), tui.Deps{
		Project:      loadProject(cmd.Context(), dir),
		RenderStatus: func(ctx context.Context) ([]string, error) { return renderStatus(ctx, dir) },
		Agents:       func(now time.Time) []tui.Agent { return tui.LoadAgents(dir, now) },
		LoadConfig:   func() (*config.Config, error) { return config.Load(dir) },
		SchedulerPID: func() int { return schedule.ReadPID(dir) },
		Now:          time.Now,
		RunOnce: func(name, focus string) error {
			_, err := schedule.SpawnRun(dir, name, focus, time.Now())
			return err
		},
		EnableCron:     func(name, expr string) error { return schedule.Enable(dir, name, expr) },
		DisableCron:    func(name string) error { return schedule.Disable(dir, name) },
		SaveCron:       func(name, expr string) error { return schedule.SaveDefaultCron(dir, name, expr) },
		StartScheduler: func() (int, error) { return schedule.StartDetached(dir) },
	})
}

func loadProject(ctx context.Context, dir string) tui.Project {
	project := tui.Project{Name: filepath.Base(dir), Dir: dir}
	if cfg, err := config.Load(dir); err == nil {
		project.Name, project.Label, project.FromAven = cfg.Tasks.Project, cfg.Tasks.Label, true
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		project.Branch = strings.TrimSpace(string(out))
	}
	return project
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show running agents, upcoming runs, and tasks that need attention",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			if err := requireVteamDir(dir, cmd.ErrOrStderr()); err != nil {
				return err
			}
			lines, err := renderStatus(cmd.Context(), dir)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), strings.Join(lines, "\n")+"\n")
			return nil
		},
	}
}

func cleanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clean",
		Short: "Remove worktrees, break stale locks, and return orphaned active tasks to todo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			clean.Run(cmd.Context(), clean.Deps{
				Cwd: dir, Out: cmd.OutOrStdout(), Cleanup: git.New(log.Discard()).CleanupOrphaned,
				Tasks: func() (tasks.Manager, error) {
					cfg, err := config.Load(dir)
					if err != nil {
						return nil, err
					}
					return tasks.NewAven(cfg.Tasks), nil
				},
				IsAlive: lock.IsProcessAlive, Host: tasks.Hostname(),
			})
			return nil
		},
	}
}

func ensureMergeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "ensure-merge",
		Short:  "Record merged PRs, cancel tasks with closed PRs, and reopen tasks whose PRs need changes or conflict",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			return ensureMerge(cmd.Context(), dir)
		},
	}
}

func ensureMerge(ctx context.Context, dir string) error {
	logger := log.Std()
	runLog, err := runlog.Start(dir, ensuremerge.Name, time.Now(), logger)
	if err != nil {
		return err
	}
	defer func() { _ = runLog.Close() }()
	logger.StartClock()
	logger.Banner("ensure-merge", "log: "+relative(dir, runLog.Path))

	err = func() error {
		cfg, err := config.Load(dir)
		if err != nil {
			return err
		}
		logger.Section("Setup")
		if err := os.MkdirAll(runstate.LocksDir(dir), 0o755); err != nil {
			return err
		}
		l, err := lock.Acquire(ctx, filepath.Join(runstate.LocksDir(dir), ensuremerge.Name), ensuremerge.Name, 30*time.Second)
		if err != nil {
			return err
		}
		defer l.Release()
		logger.Step("lock acquired")
		res, err := ensuremerge.Sweep(ctx, ensuremerge.Deps{Config: cfg, Log: logger, Tasks: tasks.NewAven(cfg.Tasks), Platform: platform.New(cfg.Platform, dir)})
		if err != nil {
			return err
		}
		ensuremerge.LogSummary(logger, res)
		return nil
	}()
	if err != nil {
		logger.WriteFile(err.Error() + "\n")
		logger.Finished(ensuremerge.Name, err.Error())
		return log.ErrReported
	}
	logger.Finished(ensuremerge.Name, "")
	return nil
}

func cronCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "cron", Short: "Enable agent crons and run them in a long-lived scheduler"}
	inProject := func(fn func(cmd *cobra.Command, dir string, args []string) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			if err := requireVteamDir(dir, cmd.ErrOrStderr()); err != nil {
				return err
			}
			return fn(cmd, dir, args)
		}
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start the scheduler for the enabled crons (runs in foreground)",
		Args:  cobra.NoArgs,
		RunE: inProject(func(cmd *cobra.Command, dir string, _ []string) error {
			return schedule.New(dir, log.Std()).Run(cmd.Context())
		}),
	}, &cobra.Command{
		Use:   "enable <agent> [cron expression]",
		Short: "Enable an agent's cron (default: the cron in its agent.json)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: inProject(func(cmd *cobra.Command, dir string, args []string) error {
			return cronEnable(cmd.OutOrStdout(), dir, args)
		}),
	}, &cobra.Command{
		Use:   "disable <agent>",
		Short: "Disable an agent's cron",
		Args:  cobra.ExactArgs(1),
		RunE: inProject(func(cmd *cobra.Command, dir string, args []string) error {
			if err := schedule.Disable(dir, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), log.FormatListItem(ansi.OK("✓"), ansi.Bold(args[0])+" cron disabled"))
			return nil
		}),
	})
	return cmd
}

func cronEnable(out io.Writer, dir string, args []string) error {
	name, expr := args[0], schedule.DefaultCron(dir, args[0])
	if len(args) == 2 {
		expr = args[1]
	}
	if expr == "" {
		return fmt.Errorf("%s has no default cron in %s; pass a cron expression", name, schedule.DefaultCronFile(name))
	}
	if err := schedule.Enable(dir, name, expr); err != nil {
		return err
	}
	now := time.Now()
	lines := []string{log.FormatListItem(ansi.OK("✓"), ansi.Bold(name)+" cron enabled "+ansi.Muted(expr))}
	if next := schedule.NextRun(expr, now); !next.IsZero() {
		lines = append(lines, log.FormatListDetail("next", log.FormatTimeOfDay(next, now)+" "+ansi.Muted("(in "+log.FormatAge(next.Sub(now))+")")))
	}
	if pid := schedule.ReadPID(dir); pid != 0 {
		lines = append(lines, log.FormatListDetail("scheduler", fmt.Sprintf("running (pid %d), picks it up within %s", pid, schedule.PollInterval)))
	} else {
		lines = append(lines, log.FormatListDetail("scheduler", ansi.Warn("not running")+" "+ansi.Muted("(start it with 'vteam cron start')")))
	}
	fmt.Fprintln(out, strings.Join(lines, "\n"))
	return nil
}

func skillCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "skill", Short: "Manage Claude Code skills from this project"}
	action := func(fn func(cwd, target string, out io.Writer) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			target, err := skill.DefaultTarget()
			if err != nil {
				return err
			}
			return fn(dir, target, cmd.OutOrStdout())
		}
	}
	cmd.AddCommand(
		&cobra.Command{Use: "install", Short: "Symlink skills from skills/ into ~/.claude/skills/", Args: cobra.NoArgs, RunE: action(skill.Install)},
		&cobra.Command{Use: "uninstall", Short: "Remove skill symlinks from ~/.claude/skills/", Args: cobra.NoArgs, RunE: action(skill.Uninstall)},
	)
	return cmd
}
