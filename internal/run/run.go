// Package run is the orchestration flow of `vteam run <agent>`: pick work, run Claude, and apply its output.
package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/leodnlv/vteam/internal/claude"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/git"
	"github.com/leodnlv/vteam/internal/lock"
	"github.com/leodnlv/vteam/internal/log"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/output"
	"github.com/leodnlv/vteam/internal/platform"
	"github.com/leodnlv/vteam/internal/prompt"
	"github.com/leodnlv/vteam/internal/runstate"
	"github.com/leodnlv/vteam/internal/tasks"
)

// Worktrees is the git work the orchestrator does; *git.Git implements it.
type Worktrees interface {
	CreateWorktree(ctx context.Context, repoRoot, taskSlug, baseBranch, worktreeDir string) (git.Session, error)
	CheckoutWorktree(ctx context.Context, repoRoot, remoteBranch, worktreeDir string) (git.Session, error)
	RemoveWorktree(ctx context.Context, repoRoot, path string)
	MergeBaseBranch(ctx context.Context, worktree, baseBranch string) ([]string, error)
	Push(ctx context.Context, worktree, branch, expectedRemoteSHA string) error
	CommitSHA(ctx context.Context, dir string) (string, error)
	HasUncommittedChanges(ctx context.Context, dir string) bool
	CommitAll(ctx context.Context, dir, subject, body string) error
}

type Deps struct {
	Cwd      string
	Config   *config.Config
	Log      *log.Logger
	Tasks    tasks.Manager
	Claude   claude.Runner
	Git      Worktrees
	Platform platform.Platform
	Now      func() time.Time
	PID      int
	Host     string
	// LockTimeout bounds the wait for the agent lock and the task claim lock.
	LockTimeout time.Duration
}

func NewDeps(cwd string, cfg *config.Config, logger *log.Logger) Deps {
	return Deps{
		Cwd:         cwd,
		Config:      cfg,
		Log:         logger,
		Tasks:       tasks.NewAven(cfg.Tasks),
		Claude:      claude.New(logger),
		Git:         git.New(logger),
		Platform:    platform.New(cfg.Platform, cwd),
		Now:         time.Now,
		PID:         os.Getpid(),
		Host:        tasks.Hostname(),
		LockTimeout: 30 * time.Second,
	}
}

// ErrFailed means the run failed and the failure was already reported in the run output.
var ErrFailed = fmt.Errorf("agent run failed: %w", log.ErrReported)

type Options struct {
	Focus string
}

func RunID(agent string, now time.Time) string {
	return now.Format("20060102-150405") + "-" + agent
}

func LocksDir(cwd string) string { return runstate.LocksDir(cwd) }

func MemoryPath(cwd, agent string) string {
	return filepath.Join(cwd, "vteam", ".memory", agent, "store.md")
}

type runner struct {
	Deps
	ctx   context.Context
	agent *config.Agent
	focus string

	runID      string
	startedAt  string
	task       *model.Task
	settled    bool
	review     *model.PRReviewContext
	followUp   *model.PRFollowUpContext
	worktree   *git.Session
	extraNotes []string
	prURL      string
	memUpdate  string
	state      model.RunState
	outcome    *model.RunOutcome
}

// Run runs agent once. It returns ErrFailed when the run failed; the failure is already in the output.
func Run(ctx context.Context, d Deps, agent *config.Agent, opts Options) error {
	r := &runner{Deps: d, ctx: ctx, agent: agent, focus: opts.Focus}
	r.runID = RunID(agent.Name, d.Now())
	r.startedAt = model.ISOTime(d.Now())
	if err := os.MkdirAll(LocksDir(d.Cwd), 0o755); err != nil {
		return err
	}

	agentLock, err := r.execute()
	if err != nil {
		r.fail(err)
	}
	r.finish()
	agentLock.Release()
	if r.outcome != nil && r.outcome.Status == "failed" {
		return ErrFailed
	}
	return nil
}

func (r *runner) writeState() {
	if err := runstate.Write(r.Cwd, r.state); err != nil {
		r.Log.Warn("failed to write run state: " + err.Error())
	}
}

func (r *runner) taskRef() *model.TaskRef {
	if r.task == nil {
		return nil
	}
	return &model.TaskRef{Title: r.task.Title, Severity: r.task.Severity}
}

func (r *runner) reviewedPR() *model.ReviewablePR {
	if r.review == nil {
		return nil
	}
	pr := r.review.PR
	return &pr
}

func (r *runner) branch() string {
	if r.worktree == nil {
		return ""
	}
	return r.worktree.Branch
}

func (r *runner) fail(err error) {
	failedAt := model.ISOTime(r.Now())
	r.state = model.RunState{RunID: r.runID, Agent: r.agent.Name, Status: model.RunFailed, StartedAt: r.startedAt, Error: err.Error()}
	r.writeState()
	r.outcome = &model.RunOutcome{
		Agent: r.agent.Name, Status: "failed", StartedAt: r.startedAt, CompletedAt: failedAt, Error: err.Error(),
		Focus: r.focus, Task: r.taskRef(), Branch: r.branch(), ReviewedPR: r.reviewedPR(),
	}
}

// execute does the run. It returns the agent lock so the caller can release it after cleanup.
func (r *runner) execute() (*lock.Lock, error) {
	r.Log.Section("Setup")
	agentLock, err := lock.Acquire(r.ctx, filepath.Join(LocksDir(r.Cwd), r.agent.Name), r.agent.Name, r.LockTimeout)
	if err != nil {
		return nil, err
	}
	r.Log.Step("lock acquired")

	if done, err := r.pickWork(); done || err != nil {
		return agentLock, err
	}

	r.state = model.RunState{RunID: r.runID, Agent: r.agent.Name, Status: model.RunStarted, StartedAt: r.startedAt}
	if r.task != nil {
		r.state.TaskRef = r.task.Ref
	}
	r.writeState()

	agentDir := r.Cwd
	if r.agent.Worktree {
		if err := r.prepareWorktree(); err != nil {
			return agentLock, err
		}
		agentDir = r.worktree.Path
	}

	out, err := r.runAgent(agentDir)
	if err != nil {
		return agentLock, err
	}
	return agentLock, r.applyOutput(out)
}

// pickWork claims a task or finds a PR to work on. done is true when there is nothing to do.
func (r *runner) pickWork() (done bool, err error) {
	switch r.agent.Input {
	case config.InputTask:
		task, err := r.claimTask()
		if err != nil {
			return true, err
		}
		if task == nil {
			r.Log.Step("no eligible tasks, nothing to do")
			return true, nil
		}
		r.task = task
		r.Log.Step(fmt.Sprintf("picked task %s: %s (%s)", task.Ref, task.Title, task.Severity))

		existing := task.Metadata[model.MetaPRURL]
		if !r.agent.Worktree || existing == "" {
			return false, nil
		}
		pr, err := r.Platform.PullRequest(r.ctx, existing)
		if err != nil {
			return true, err
		}
		if pr.State != model.PROpen {
			return true, r.parkTaskWithClosedPR(pr)
		}
		threads, err := r.Platform.OpenReviewThreads(r.ctx, pr.Number)
		if err != nil {
			return true, err
		}
		r.followUp = &model.PRFollowUpContext{PR: pr, Threads: threads}
		r.prURL = pr.URL
		r.Log.Step(fmt.Sprintf("following up on PR #%d (%d open review threads)", pr.Number, len(threads)))

	case config.InputPR:
		var labels []string
		if r.agent.PRTriggerLabel != "" {
			labels = []string{r.agent.PRTriggerLabel}
		}
		pr, err := r.Platform.FindReviewablePR(r.ctx, labels)
		if err != nil {
			return true, err
		}
		if pr == nil {
			r.Log.Step("no PRs need a review response, nothing to do")
			return true, nil
		}
		comments, err := r.Platform.ReviewComments(r.ctx, pr.Number)
		if err != nil {
			return true, err
		}
		slug, err := r.Platform.RepoSlug(r.ctx)
		if err != nil {
			return true, err
		}
		r.review = &model.PRReviewContext{PR: *pr, Comments: comments, RepoSlug: slug}
		suffix := ""
		if len(comments) > 0 {
			suffix = fmt.Sprintf(" (%d comments)", len(comments))
		}
		r.Log.Step(fmt.Sprintf("picked PR #%d: %s%s", pr.Number, pr.Title, suffix))
	}
	return false, nil
}

func (r *runner) claimTask() (*model.Task, error) {
	claimLock, err := lock.Acquire(r.ctx, filepath.Join(LocksDir(r.Cwd), "task-claim"), r.agent.Name, r.LockTimeout)
	if err != nil {
		return nil, err
	}
	defer claimLock.Release()
	ready, err := r.Tasks.ListReady(r.ctx)
	if err != nil {
		return nil, err
	}
	task, ok := tasks.PickNext(ready, r.agent.TaskFilter, r.Config.Tasks.MaxRetries)
	if !ok {
		return nil, nil
	}
	if err := r.Tasks.Update(r.ctx, task.Ref, tasks.Update{Status: model.StatusActive, Metadata: tasks.ClaimMetadata(r.runID, r.PID, r.Host)}); err != nil {
		return nil, err
	}
	return &task, nil
}

func (r *runner) parkTaskWithClosedPR(pr model.PullRequestInfo) error {
	r.Log.Warn(fmt.Sprintf("PR %s is %s, moving task to backlog", pr.URL, pr.State))
	if err := r.Tasks.Update(r.ctx, r.task.Ref, tasks.Update{Status: model.StatusBacklog, RemoveMetadata: tasks.ClaimKeys}); err != nil {
		return err
	}
	r.settled = true
	r.addNote(r.task.Ref, fmt.Sprintf("%s: skipped\n\nPR %s is %s, so there is no branch to push to. Remove the pr-url metadata and move the task to todo to start over on a new branch.", r.agent.Name, pr.URL, pr.State))
	return nil
}

func (r *runner) prepareWorktree() error {
	var session git.Session
	var err error
	switch {
	case r.review != nil:
		r.Log.Step("checking out PR branch " + r.review.PR.Branch)
		session, err = r.Git.CheckoutWorktree(r.ctx, r.Cwd, r.review.PR.Branch, r.Config.WorktreeDir)
	case r.followUp != nil:
		r.Log.Step("checking out PR branch " + r.followUp.PR.Branch)
		session, err = r.Git.CheckoutWorktree(r.ctx, r.Cwd, r.followUp.PR.Branch, r.Config.WorktreeDir)
	default:
		slug := r.agent.Name + "-" + r.runID
		if r.task != nil {
			slug = r.task.Title + "-" + r.runID
		}
		session, err = r.Git.CreateWorktree(r.ctx, r.Cwd, slug, r.Config.BaseBranch, r.Config.WorktreeDir)
	}
	if err != nil {
		return err
	}
	r.worktree = &session

	if r.followUp != nil && r.followUp.PR.Mergeable == model.Conflicting {
		base := r.followUp.PR.BaseBranch
		r.Log.Warn(fmt.Sprintf("PR conflicts with %s, merging origin/%s", base, base))
		conflicted, err := r.Git.MergeBaseBranch(r.ctx, session.Path, base)
		if err != nil {
			return err
		}
		r.followUp.BaseMerge = &model.BaseBranchMerge{BaseBranch: base, ConflictedFiles: conflicted}
		if len(conflicted) > 0 {
			r.Log.Warn("conflicts in " + strings.Join(conflicted, ", "))
		} else {
			r.Log.OK("merged cleanly")
		}
	}

	r.state.WorktreePath = session.Path
	r.state.BranchName = session.Branch
	r.writeState()
	r.Log.Step("worktree ready")
	r.Log.Field("path", relative(r.Cwd, session.Path))
	r.Log.Field("branch", session.Branch)
	return nil
}

func relative(base, path string) string {
	if rel, err := filepath.Rel(base, path); err == nil {
		return rel
	}
	return path
}

func (r *runner) readMemory() string {
	if r.agent.Memory == nil {
		return ""
	}
	data, err := os.ReadFile(MemoryPath(r.Cwd, r.agent.Name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (r *runner) runAgent(dir string) (model.AgentOutput, error) {
	idx, err := r.Tasks.GetIndex(r.ctx)
	if err != nil {
		return model.AgentOutput{}, err
	}
	parts, err := prompt.Build(prompt.Input{
		Agent: r.agent, Existing: idx.All, Task: r.task, Review: r.review, FollowUp: r.followUp,
		Focus: r.focus, Memory: r.readMemory(),
	})
	if err != nil {
		return model.AgentOutput{}, err
	}
	r.Log.Step("prompt built")

	title := "Agent: " + r.agent.Name
	if r.agent.Model != "" {
		title += " (" + r.agent.Model + ")"
	}
	r.Log.Section(title)
	r.state.Status = model.RunClaudeRunning
	r.writeState()

	res, err := r.Claude.Run(r.ctx, claude.Options{
		SystemPrompt: parts.SystemPrompt, UserPrompt: parts.UserPrompt, Dir: dir, Model: r.agent.Model,
		AllowedTools: r.agent.AllowedTools, DisallowedTools: r.agent.DisallowedTools,
	})
	if err != nil {
		return model.AgentOutput{}, err
	}
	if res.ExitCode != 0 {
		return model.AgentOutput{}, fmt.Errorf("claude exited with code %d", res.ExitCode)
	}
	if res.ResultText == "" {
		return model.AgentOutput{}, errors.New("claude produced no output")
	}

	r.state.Status = model.RunProcessing
	r.writeState()
	r.Log.Section("Result")
	out, err := output.Parse(res.ResultText)
	if err != nil {
		return out, err
	}
	r.memUpdate = out.MemoryUpdate
	r.state.ClaudeOutput = &out
	r.state.CommitMessage = out.CommitMessage
	return out, nil
}

func (r *runner) commitsAgentChanges(out model.AgentOutput, unresolved []string) bool {
	return out.CommitMessage != nil &&
		(out.Status == model.OutputCompleted || out.Status == model.OutputPartial) &&
		len(unresolved) == 0 &&
		r.Git.HasUncommittedChanges(r.ctx, r.worktree.Path)
}

func (r *runner) mergedBaseCleanly() bool {
	return r.followUp != nil && r.followUp.BaseMerge != nil && len(r.followUp.BaseMerge.ConflictedFiles) == 0
}

func (r *runner) applyOutput(out model.AgentOutput) error {
	if out.Content != nil && out.Content.Type == model.ContentTask {
		body := *out.Content.Task
		ref, err := r.Tasks.Create(r.ctx, body, r.agent.Name, tasks.LabelsOnCreate(r.Config.Tasks, r.agent.Name))
		if err != nil {
			return err
		}
		r.state.TasksCreated = []string{ref}
		r.Log.OK(fmt.Sprintf("created task %s: %s (%s)", ref, body.Title, body.Severity))
	}

	var unresolved []string
	if r.worktree != nil && r.followUp != nil && r.followUp.BaseMerge != nil {
		unresolved = git.FindUnresolvedConflicts(r.worktree.Path, r.followUp.BaseMerge.ConflictedFiles)
	}
	if len(unresolved) > 0 {
		r.extraNotes = append(r.extraNotes, fmt.Sprintf("Unresolved merge conflicts in: %s. Nothing was committed.", strings.Join(unresolved, ", ")))
	}
	var commitMessage *model.CommitMessage
	if r.worktree != nil && r.commitsAgentChanges(out, unresolved) {
		commitMessage = out.CommitMessage
	}

	switch {
	case r.worktree != nil && r.agent.ReadOnly:
		r.Log.Step("read-only agent, nothing committed")
		if r.review != nil {
			r.removeTriggerLabel(r.review.PR.Number)
		}
	case r.worktree != nil && (commitMessage != nil || r.mergedBaseCleanly()):
		if err := r.publish(out, commitMessage); err != nil {
			return err
		}
	case r.worktree != nil:
		if err := r.recordNoChanges(out, unresolved); err != nil {
			return err
		}
	}

	if r.task != nil {
		r.addNote(r.task.Ref, formatRunNote(r.agent.Name, out, r.prURL, r.extraNotes))
	}

	completedAt := model.ISOTime(r.Now())
	r.state.Status = model.RunCompleted
	r.state.CompletedAt = completedAt
	r.writeState()
	r.outcome = &model.RunOutcome{
		Agent: r.agent.Name, Status: "completed", StartedAt: r.startedAt, CompletedAt: completedAt,
		Focus: r.focus, Task: r.taskRef(), Branch: r.branch(), PRURL: r.prURL, ReviewedPR: r.reviewedPR(),
		TasksCreated: r.state.TasksCreated, CommitMessage: r.state.CommitMessage, Content: out.Content,
	}
	return nil
}

// publish commits the agent's changes (or pushes a clean base merge), pushes, and settles PR and task.
func (r *runner) publish(out model.AgentOutput, commitMessage *model.CommitMessage) error {
	wt := r.worktree
	if commitMessage != nil {
		if err := r.Git.CommitAll(r.ctx, wt.Path, commitMessage.Subject, commitMessage.Body); err != nil {
			return err
		}
		r.Log.OK("committed " + commitMessage.Subject)
	} else {
		r.Log.Step("no changes, pushing the merge of " + r.followUp.BaseMerge.BaseBranch)
	}
	sha, err := r.Git.CommitSHA(r.ctx, wt.Path)
	if err != nil {
		return err
	}
	r.state.CommitSHA = sha
	if err := r.Git.Push(r.ctx, wt.Path, wt.Branch, wt.RemoteSHA); err != nil {
		return err
	}
	r.Log.OK("pushed " + wt.Branch)

	if r.review != nil {
		number := r.review.PR.Number
		if err := r.Platform.PostComment(r.ctx, number, fmt.Sprintf("Addressed review feedback in `%s`.", short(sha))); err != nil {
			r.Log.Warn("failed to post PR comment: " + err.Error())
		} else {
			r.Log.OK(fmt.Sprintf("commented on PR #%d", number))
		}
		r.removeTriggerLabel(number)
	}

	switch {
	case r.followUp != nil:
		r.Log.OK("updated PR " + r.followUp.PR.URL)
		if len(out.ThreadReplies) > 0 {
			posted := r.postThreadReplies(out.ThreadReplies, sha)
			note := fmt.Sprintf("Replied to %d of %d review threads.", posted, len(out.ThreadReplies))
			r.extraNotes = append(r.extraNotes, note)
			r.Log.Step(note)
		}
	case r.agent.AutoPR:
		r.createPR(out)
	}

	if r.task != nil {
		err := r.Tasks.Update(r.ctx, r.task.Ref, tasks.Update{
			Status:         model.StatusDone,
			Metadata:       map[string]string{model.MetaCompleted: model.ISOTime(r.Now()), model.MetaBranch: wt.Branch, model.MetaPRURL: r.prURL},
			RemoveMetadata: tasks.ClaimKeys,
			AddLabels:      tasks.LabelsOnDone(r.Config.Tasks, r.agent.Name),
		})
		if err != nil {
			return err
		}
		r.settled = true
		r.Log.OK(fmt.Sprintf("task %s done", r.task.Ref))
	} else if r.prURL != "" {
		if ref := r.recordPRTask(out); ref != "" {
			r.state.TasksCreated = append(r.state.TasksCreated, ref)
		}
	}
	return nil
}

var vteamPrefix = regexp.MustCompile(`(?i)^vteam:\s*`)

func (r *runner) createPR(out model.AgentOutput) {
	body := fmt.Sprintf("Automated by vteam (%s).", r.agent.Name)
	if r.task != nil {
		body += "\n\nTask: " + r.task.Title
	}
	if out.CommitMessage != nil && out.CommitMessage.Body != "" {
		body = out.CommitMessage.Body
	}
	summary := ""
	if r.task != nil {
		summary = r.task.Title
	} else if out.CommitMessage != nil {
		summary = vteamPrefix.ReplaceAllString(out.CommitMessage.Subject, "")
	}
	title := "vteam: " + r.agent.Name
	if summary != "" {
		title += ": " + summary
	}
	url, err := r.Platform.CreatePR(r.ctx, platform.PRRequest{
		Branch: r.worktree.Branch, BaseBranch: r.Config.BaseBranch, Title: title, Body: body, Labels: r.agent.PRCreateLabels,
	})
	if err != nil {
		r.Log.Warn("PR creation failed: " + err.Error())
		r.Log.Warn("the branch was pushed, create the PR manually")
		return
	}
	r.prURL = url
	r.Log.OK("created PR " + url)
}

func (r *runner) recordNoChanges(out model.AgentOutput, unresolved []string) error {
	r.Log.Warn(fmt.Sprintf("%s produced no changes (%s)", r.agent.Name, out.Status))
	if out.Status == model.OutputBlocked || out.Status == model.OutputFailed {
		reason := out.BlockerReason
		if reason == "" {
			reason = out.Summary
		}
		r.Log.Field("reason", reason)
	}
	if len(unresolved) > 0 {
		r.Log.Field("conflicts", strings.Join(unresolved, ", "))
	}
	if r.task == nil {
		return nil
	}
	retries := r.task.RetryCount + 1
	err := r.Tasks.Update(r.ctx, r.task.Ref, tasks.Update{
		Status:         model.StatusTodo,
		Metadata:       map[string]string{model.MetaRetryCount: fmt.Sprint(retries)},
		RemoveMetadata: tasks.ClaimKeys,
	})
	if err != nil {
		return err
	}
	r.settled = true
	r.Log.Field("retries", fmt.Sprintf("%d/%d", retries, r.Config.Tasks.MaxRetries))
	return nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func (r *runner) removeTriggerLabel(number int) {
	label := r.agent.PRTriggerLabel
	if label == "" {
		return
	}
	if err := r.Platform.RemoveLabel(r.ctx, number, label); err != nil {
		r.Log.Warn(fmt.Sprintf("failed to remove label %q from PR #%d: %s", label, number, err))
		return
	}
	r.Log.OK(fmt.Sprintf("removed label %q from PR #%d", label, number))
}

func (r *runner) postThreadReplies(replies []model.ThreadReply, sha string) int {
	threads := map[string]model.ReviewThread{}
	for i, t := range r.followUp.Threads {
		threads[prompt.ThreadRef(i)] = t
	}
	posted := 0
	number := r.followUp.PR.Number
	for _, reply := range replies {
		thread, ok := threads[reply.Thread]
		if !ok {
			r.Log.Warn("skipping reply to unknown review thread " + reply.Thread)
			continue
		}
		body := fmt.Sprintf("🤖 **vteam %s:** %s\n\nFixed in %s.", r.agent.Name, reply.Body, short(sha))
		if err := r.Platform.ReplyToThread(r.ctx, number, thread.ID, body); err != nil {
			r.Log.Warn(fmt.Sprintf("failed to reply to review thread %s: %s", reply.Thread, err))
			continue
		}
		posted++
		if r.agent.ResolveThreads {
			if err := r.Platform.ResolveThread(r.ctx, number, thread.ID); err != nil {
				r.Log.Warn(fmt.Sprintf("failed to resolve review thread %s: %s", reply.Thread, err))
			}
		}
	}
	return posted
}

// recordPRTask creates a done task for a PR opened without one, because the merge ensurer only tracks PRs through tasks.
func (r *runner) recordPRTask(out model.AgentOutput) string {
	title, description := out.Summary, out.Summary
	if out.CommitMessage != nil {
		if subject := vteamPrefix.ReplaceAllString(out.CommitMessage.Subject, ""); subject != "" {
			title = subject
		}
		if out.CommitMessage.Body != "" {
			description = out.CommitMessage.Body
		}
	}
	files := out.FilesChanged
	if files == nil {
		files = []string{}
	}
	ref, err := r.Tasks.Create(r.ctx, model.TaskContentBody{Title: title, Severity: model.SeverityLow, Description: description, Files: files},
		r.agent.Name, tasks.LabelsOnCreate(r.Config.Tasks, r.agent.Name))
	if err == nil {
		err = r.Tasks.Update(r.ctx, ref, tasks.Update{
			Status:    model.StatusDone,
			Metadata:  map[string]string{model.MetaCompleted: model.ISOTime(r.Now()), model.MetaBranch: r.worktree.Branch, model.MetaPRURL: r.prURL},
			AddLabels: tasks.LabelsOnDone(r.Config.Tasks, r.agent.Name),
		})
	}
	if err != nil {
		r.Log.Warn(fmt.Sprintf("failed to record a task for %s, the merge ensurer will not track it: %s", r.prURL, err))
		return ""
	}
	r.Log.OK(fmt.Sprintf("recorded task %s for %s", ref, r.prURL))
	r.addNote(ref, formatRunNote(r.agent.Name, out, r.prURL, nil))
	return ref
}

func formatRunNote(agent string, out model.AgentOutput, prURL string, extra []string) string {
	parts := []string{fmt.Sprintf("%s: %s", agent, out.Status), out.Summary}
	if out.BlockerReason != "" {
		parts = append(parts, "Blocker: "+out.BlockerReason)
	}
	parts = append(parts, extra...)
	if prURL != "" {
		parts = append(parts, "PR: "+prURL)
	}
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}

func (r *runner) addNote(ref, text string) {
	if err := r.Tasks.AddNote(r.ctx, ref, text); err != nil {
		r.Log.Warn(fmt.Sprintf("failed to add note to task %s: %s", ref, err))
	}
}

// finish releases an unsettled task, runs the hooks, and removes the worktree, whatever happened before.
func (r *runner) finish() {
	if r.task != nil && !r.settled {
		if err := r.Tasks.Update(r.ctx, r.task.Ref, tasks.Update{Status: model.StatusTodo, RemoveMetadata: tasks.ClaimKeys}); err != nil {
			r.Log.Warn(fmt.Sprintf("failed to return task %s to todo: %s", r.task.Ref, err))
		}
	}
	if r.outcome != nil {
		r.runOnFinishHook()
		r.runMemoryCuration()
	}
	if r.worktree != nil {
		r.Log.Section("Cleanup")
		r.Git.RemoveWorktree(r.ctx, r.Cwd, r.worktree.Path)
		r.Log.Step("worktree removed")
	}
	if r.outcome != nil {
		failure := ""
		if r.outcome.Status == "failed" {
			failure = r.outcome.Error
		}
		r.Log.Finished(r.agent.Name, failure)
	}
}

func sectionTitle(name, model string) string {
	if model != "" {
		return name + " (" + model + ")"
	}
	return name
}

func (r *runner) runOnFinishHook() {
	hook := r.agent.OnFinish
	if hook == nil {
		return
	}
	r.Log.Section(sectionTitle("On-finish hook", hook.Model))
	parts, err := prompt.OnFinish(hook, *r.outcome)
	if err == nil {
		_, err = r.Claude.Run(r.ctx, claude.Options{SystemPrompt: parts.SystemPrompt, UserPrompt: parts.UserPrompt, Dir: r.Cwd,
			Model: hook.Model, AllowedTools: hook.AllowedTools, DisallowedTools: hook.DisallowedTools})
	}
	if err != nil {
		r.Log.Warn("on-finish hook failed: " + err.Error())
	}
}

func (r *runner) runMemoryCuration() {
	mem := r.agent.Memory
	if mem == nil || r.memUpdate == "" {
		return
	}
	r.Log.Section(sectionTitle("Memory curation", mem.Model))
	path := MemoryPath(r.Cwd, r.agent.Name)
	current, _ := os.ReadFile(path)
	parts, err := prompt.MemoryCuration(mem, string(current), r.memUpdate, r.focus)
	if err != nil {
		r.Log.Warn("memory curation failed: " + err.Error())
		return
	}
	res, err := r.Claude.Run(r.ctx, claude.Options{SystemPrompt: parts.SystemPrompt, UserPrompt: parts.UserPrompt, Dir: r.Cwd,
		Model: mem.Model, AllowedTools: mem.AllowedTools, DisallowedTools: mem.DisallowedTools})
	if err != nil {
		r.Log.Warn("memory curation failed: " + err.Error())
		return
	}
	if res.ExitCode != 0 || res.ResultText == "" {
		r.Log.Warn("memory curation produced no output or failed")
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		err = os.WriteFile(path, []byte(strings.TrimSpace(res.ResultText)+"\n"), 0o644)
	}
	if err != nil {
		r.Log.Warn("memory curation failed: " + err.Error())
		return
	}
	r.Log.OK("memory updated")
}
