// Package status renders vteam status: running agents, the scheduler, next picks, and tasks that need attention.
package status

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/leodnlv/vteam/internal/ansi"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/runstate"
	"github.com/leodnlv/vteam/internal/schedule"
	"github.com/leodnlv/vteam/internal/tasks"
	"golang.org/x/sync/errgroup"
)

type Deps struct {
	Cwd     string
	Config  *config.Config
	Tasks   tasks.Manager
	Now     func() time.Time
	Host    string
	IsAlive func(pid int) bool
	// Orphans lists worktrees under worktreeDir that no path in inUse refers to.
	Orphans func(ctx context.Context, repoRoot, worktreeDir string, inUse []string) ([]string, error)
}

type runningJob struct {
	runstate.LiveRun
	task *model.Task
}

type loadedAgents struct {
	agents []*config.Agent
	errors []string
}

func Render(ctx context.Context, d Deps) ([]string, error) {
	var idx tasks.Index
	var ready []model.Task
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { idx, err = d.Tasks.GetIndex(gctx); return })
	g.Go(func() (err error) { ready, err = d.Tasks.ListReady(gctx); return })
	if err := g.Wait(); err != nil {
		return nil, err
	}
	loaded := loadAgents(d.Cwd)
	running := findRunningJobs(d, idx)
	now := d.Now()

	lines := []string{log.FormatBanner("status", fmt.Sprintf("aven: %s · label: %s", d.Config.Tasks.Project, d.Config.Tasks.Label))}
	lines = append(lines, renderRunning(d.Cwd, running, now)...)
	lines = append(lines, schedule.RenderScheduler(d.Cwd, now)...)
	lines = append(lines, renderUpNext(loaded.agents, ready, d.Config)...)
	lines = append(lines, renderAwaitingMerge(idx)...)
	lines = append(lines, renderNeedsAttention(ctx, d, loaded, idx, running)...)
	return lines, nil
}

func loadAgents(cwd string) loadedAgents {
	var l loadedAgents
	for _, name := range config.ListAgentNames(cwd) {
		agent, err := config.ResolveAgent(name, cwd)
		if err != nil {
			l.errors = append(l.errors, fmt.Sprintf("agent %q failed to load: %s", name, err))
			continue
		}
		l.agents = append(l.agents, agent)
	}
	return l
}

func findRunningJobs(d Deps, idx tasks.Index) []runningJob {
	var jobs []runningJob
	for _, run := range runstate.FindLive(d.Cwd) {
		job := runningJob{LiveRun: run}
		for _, t := range idx.ByStatus[model.StatusActive] {
			if t.Metadata[model.MetaClaimedHost] == d.Host && t.Metadata[model.MetaClaimedPID] == strconv.Itoa(run.Lock.PID) {
				job.task = &t
				break
			}
		}
		jobs = append(jobs, job)
	}
	return jobs
}

func relative(base, path string) string {
	if rel, err := filepath.Rel(base, path); err == nil {
		return rel
	}
	return path
}

func renderRunning(cwd string, running []runningJob, now time.Time) []string {
	lines := []string{log.FormatSection("Running")}
	if len(running) == 0 {
		return append(lines, log.FormatListItem(ansi.Muted("·"), ansi.Dim("nothing running")))
	}
	width := 0
	for _, r := range running {
		width = max(width, len(r.Name))
	}
	for _, job := range running {
		meta := ansi.Muted(fmt.Sprintf("pid %d · %s", job.Lock.PID, log.FormatAge(now.Sub(job.Lock.Time()))))
		task := ""
		if job.task != nil {
			task = "  " + formatTask(*job.task)
		}
		lines = append(lines, log.FormatListItem(ansi.Live("●"), ansi.Bold(log.PadEnd(job.Name, width))+"  "+meta+task))
		if job.State != nil && job.State.WorktreePath != "" {
			lines = append(lines, log.FormatListDetail("worktree", relative(cwd, job.State.WorktreePath)))
		}
	}
	return lines
}

func renderUpNext(agents []*config.Agent, ready []model.Task, cfg *config.Config) []string {
	var pickers []*config.Agent
	width := 0
	for _, a := range agents {
		if a.Input != "" {
			pickers = append(pickers, a)
			width = max(width, len(a.Name))
		}
	}
	if len(pickers) == 0 {
		return nil
	}
	lines := []string{log.FormatSection("Up next")}
	for _, a := range pickers {
		lines = append(lines, log.FormatListItem(ansi.Info("•"), ansi.Bold(log.PadEnd(a.Name, width))+"  "+describeNextPick(a, ready, cfg)))
	}
	return lines
}

func describeNextPick(a *config.Agent, ready []model.Task, cfg *config.Config) string {
	if a.Input == config.InputPR {
		if a.PRTriggerLabel != "" {
			return fmt.Sprintf("open PRs labelled %q", a.PRTriggerLabel)
		}
		return "any open PR with review feedback"
	}
	task, ok := tasks.PickNext(ready, a.TaskFilter, cfg.Tasks.MaxRetries)
	if !ok {
		return ansi.Dim("nothing ready")
	}
	followUp := ""
	if a.Worktree && task.Metadata[model.MetaPRURL] != "" {
		followUp = ansi.Muted(" (PR follow-up)")
	}
	return formatTask(task) + followUp
}

func renderAwaitingMerge(idx tasks.Index) []string {
	var awaiting []model.Task
	for _, t := range idx.ByStatus[model.StatusDone] {
		if t.Metadata[model.MetaPRURL] != "" && t.Metadata[model.MetaMerged] != "true" {
			awaiting = append(awaiting, t)
		}
	}
	if len(awaiting) == 0 {
		return nil
	}
	lines := []string{log.FormatSection(fmt.Sprintf("Awaiting merge (%d)", len(awaiting)))}
	for _, t := range awaiting {
		lines = append(lines, log.FormatListItem(ansi.Info("•"), ansi.Bold(t.Ref)+" "+t.Title+"  "+ansi.Muted(t.Metadata[model.MetaPRURL])))
	}
	return lines
}

func renderNeedsAttention(ctx context.Context, d Deps, loaded loadedAgents, idx tasks.Index, running []runningJob) []string {
	var items []string
	warning := func(text string) { items = append(items, log.FormatListItem(ansi.Warn("!"), text)) }
	failure := func(text string) { items = append(items, log.FormatListItem(ansi.Fail("✗"), text)) }

	for _, e := range loaded.errors {
		failure(e)
	}
	if n := len(idx.ByStatus[model.StatusInbox]); n > 0 {
		warning(fmt.Sprintf("%d task(s) in inbox awaiting triage", n))
	}

	var taskAgents []*config.Agent
	for _, a := range loaded.agents {
		if a.Input == config.InputTask {
			taskAgents = append(taskAgents, a)
		}
	}
	for _, t := range idx.ByStatus[model.StatusTodo] {
		ref := ansi.Bold(t.Ref)
		routed := false
		for _, a := range taskAgents {
			if tasks.MatchesFilter(t, a.TaskFilter) {
				routed = true
				break
			}
		}
		switch {
		case !routed:
			failure(ref + " matches no agent's taskFilter and will never be picked")
		case t.RetryCount >= d.Config.Tasks.MaxRetries:
			failure(fmt.Sprintf("%s hit max retries (%d/%d) and will not be picked", ref, t.RetryCount, d.Config.Tasks.MaxRetries))
		}
	}

	for _, t := range idx.ByStatus[model.StatusActive] {
		ref := ansi.Bold(t.Ref)
		pid := tasks.ClaimedPID(t)
		switch {
		case pid == 0:
			warning(ref + " is active without a vteam claim " + ansi.Muted("(reset it by hand if it is stuck)"))
		case t.Metadata[model.MetaClaimedHost] == d.Host && !d.IsAlive(pid):
			failure(fmt.Sprintf("%s is active but its process (pid %d) is dead %s", ref, pid, ansi.Muted("(run 'vteam clean')")))
		}
	}

	var inUse []string
	for _, r := range running {
		if r.State != nil && r.State.WorktreePath != "" {
			inUse = append(inUse, r.State.WorktreePath)
		}
	}
	if leftovers, err := d.Orphans(ctx, d.Cwd, d.Config.WorktreeDir, inUse); err == nil && len(leftovers) > 0 {
		lines := []string{fmt.Sprintf("%d worktree(s) with no running agent %s", len(leftovers), ansi.Muted("(run 'vteam clean')"))}
		for _, p := range leftovers {
			lines = append(lines, ansi.Muted(relative(d.Cwd, p)))
		}
		warning(strings.Join(lines, "\n"))
	}

	if len(items) == 0 {
		return nil
	}
	return append([]string{log.FormatSection("Needs attention")}, items...)
}

var severityColor = map[model.Severity]func(string) string{
	model.SeverityCritical: func(s string) string { return ansi.Bold(ansi.Fail(s)) },
	model.SeverityHigh:     ansi.Fail,
	model.SeverityMedium:   ansi.Warn,
	model.SeverityLow:      ansi.Muted,
}

func formatTask(t model.Task) string {
	paint, ok := severityColor[t.Severity]
	if !ok {
		paint = ansi.Muted
	}
	return ansi.Bold(t.Ref) + " " + paint("["+string(t.Severity)+"]") + " " + t.Title
}
