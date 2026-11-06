package run

import (
	"context"
	"fmt"
	"os"

	"github.com/leodnlv/vteam/internal/claude"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/git"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/prompt"
	"github.com/leodnlv/vteam/internal/session"
	"github.com/leodnlv/vteam/internal/slugify"
	"github.com/leodnlv/vteam/internal/workmux"
)

// prepareSession creates the branch and the workmux worktree an interactive session runs in. Follow-ups and PR
// reviews work on a uniquely named local branch that pushes to the PR branch, so an abandoned worktree still
// holding the PR branch never blocks the next session.
func (r *runner) prepareSession() error {
	var branch, pushBranch, from string
	switch {
	case r.review != nil:
		pushBranch = r.review.PR.Branch
	case r.followUp != nil:
		pushBranch = r.followUp.PR.Branch
	}
	if pushBranch != "" {
		branch, from = "vteam-session/"+r.runID, pushBranch
		r.Log.Step("checking out PR branch " + pushBranch)
	} else {
		slug := r.agent.Name + "-" + r.runID
		if r.task != nil {
			slug = r.task.Title + "-" + r.runID
		}
		branch, from = "vteam/"+slugify.Slugify(slug), r.Config.BaseBranch
		pushBranch = branch
	}
	sha, err := r.Git.CreateBranch(r.ctx, r.Cwd, branch, from)
	if err != nil {
		return err
	}
	remoteSHA := ""
	if pushBranch != branch {
		remoteSHA = sha
	}
	wt, err := r.Workmux.Add(r.ctx, r.Cwd, branch)
	if err != nil {
		return err
	}
	r.handle, r.localBranch = wt.Handle, branch
	r.scratch = !r.agent.Worktree
	r.worktree = &git.Session{Path: wt.Path, Branch: pushBranch, RemoteSHA: remoteSHA}
	if err := r.mergeBaseIfConflicting(); err != nil {
		return err
	}
	if r.startSHA, err = r.Git.CommitSHA(r.ctx, wt.Path); err != nil {
		return err
	}
	r.worktreeReady()
	return nil
}

func (r *runner) submitCommand() string {
	return fmt.Sprintf("%s submit --project %s --session %s", claude.ShellQuote(r.Bin), claude.ShellQuote(r.Cwd), r.runID)
}

func (r *runner) session(status session.Status) session.Session {
	s := session.Session{
		RunID: r.runID, Agent: r.agent.Name, Status: status, StartedAt: r.startedAt, Handle: r.handle,
		Task: r.task, Review: r.review, FollowUp: r.followUp, Focus: r.focus, LogPath: r.logPath,
		StartSHA: r.startSHA, Scratch: r.scratch, Branch: r.localBranch,
	}
	if r.worktree != nil {
		s.WorktreePath, s.PushBranch, s.RemoteSHA = r.worktree.Path, r.worktree.Branch, r.worktree.RemoteSHA
	}
	return s
}

// handOff builds the prompt and opens it in an interactive Claude session. The run ends with the session open:
// the task stays claimed and the worktree stays until `vteam submit` settles it.
func (r *runner) handOff() error {
	idx, err := r.Tasks.GetIndex(r.ctx)
	if err != nil {
		return err
	}
	parts, err := prompt.Build(prompt.Input{
		Agent: r.agent, Existing: idx.All, Task: r.task, Review: r.review, FollowUp: r.followUp,
		Focus: r.focus, Memory: r.readMemory(), SubmitCommand: r.submitCommand(),
	})
	if err != nil {
		return err
	}
	r.Log.Step("prompt built")

	if err := session.EnsureDir(r.Cwd, r.runID); err != nil {
		return err
	}
	systemFile := session.SystemPromptPath(r.Cwd, r.runID)
	promptFile := session.PromptPath(r.Cwd, r.runID)
	configFile := session.WorkmuxConfigPath(r.Cwd, r.runID)
	command := claude.InteractiveCommand(systemFile, claude.Options{
		Model: r.agent.Model, AllowedTools: r.agent.AllowedTools, DisallowedTools: r.agent.DisallowedTools,
	})
	files := map[string]string{systemFile: parts.SystemPrompt, promptFile: parts.UserPrompt, configFile: workmux.AgentConfig(command)}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
	}
	sess := r.session(session.Open)
	sess.InUserSession = r.InTmux
	if err := session.Save(r.Cwd, sess); err != nil {
		return err
	}

	r.Log.Section(sectionTitle("Interactive session: "+r.agent.Name, r.agent.Model))
	open := workmux.OpenOptions{Handle: r.handle, PromptFile: promptFile, ConfigFile: configFile}
	if !r.InTmux {
		open.ParentSession = TmuxSession(r.Config)
	}
	if err := r.Workmux.Open(r.ctx, r.Cwd, open); err != nil {
		return err
	}
	r.handedOff = true
	r.state.Status = model.RunInteractive
	r.writeState()
	r.Log.OK("session open in workmux window " + r.handle)
	if open.ParentSession != "" {
		r.Log.Field("tmux", "session "+open.ParentSession+" (attach with 'tmux attach -t "+open.ParentSession+"')")
	}
	r.Log.Field("submit", "/vteam-submit in the session")
	r.Log.Field("abandon", "close the window; the merge ensurer releases the task")
	return nil
}

// fromSession rebuilds the runner of an interactive run from its saved session.
func fromSession(ctx context.Context, d Deps, agent *config.Agent, s session.Session) *runner {
	r := &runner{
		Deps: d, ctx: context.WithoutCancel(ctx), stopCtx: ctx, agent: agent, focus: s.Focus, logPath: s.LogPath,
		interactive: true, handle: s.Handle, localBranch: s.Branch, startSHA: s.StartSHA, scratch: s.Scratch,
		runID: s.RunID, startedAt: s.StartedAt, task: s.Task, review: s.Review, followUp: s.FollowUp,
		worktree: &git.Session{Path: s.WorktreePath, Branch: s.PushBranch, RemoteSHA: s.RemoteSHA},
	}
	r.state = model.RunState{
		RunID: s.RunID, Agent: agent.Name, Status: model.RunProcessing, StartedAt: s.StartedAt,
		WorktreePath: s.WorktreePath, BranchName: s.PushBranch,
	}
	if s.Task != nil {
		r.state.TaskRef = s.Task.Ref
	}
	return r
}

// Submit applies the output of an interactive session: tasks, commit, push, PR, and the task update. On an error
// the session stays open with its task claimed, so it can be submitted again. It returns the session ready for
// Finalize.
func Submit(ctx context.Context, d Deps, agent *config.Agent, s session.Session, out model.AgentOutput) (session.Session, error) {
	r := fromSession(ctx, d, agent, s)
	r.Log.Section("Submit")
	r.memUpdate = out.MemoryUpdate
	r.state.ClaudeOutput = &out
	r.state.CommitMessage = out.CommitMessage
	if err := r.applyOutput(out); err != nil {
		r.Log.Fail(err.Error())
		return s, err
	}
	r.releaseTask()
	s.Status = session.Submitting
	s.Outcome = r.outcome
	s.MemoryUpdate = out.MemoryUpdate
	return s, session.Save(d.Cwd, s)
}

// Finalize runs the on-finish hook and memory curation of a submitted session, then removes its worktree and local
// branch. A window in the user's own tmux session is closed too; one in the dedicated vteam session stays for the
// user to close, because killing a session's last window ends the session and detaches whoever is attached.
func Finalize(ctx context.Context, d Deps, agent *config.Agent, s session.Session) error {
	r := fromSession(ctx, d, agent, s)
	r.outcome, r.memUpdate = s.Outcome, s.MemoryUpdate
	if r.outcome != nil {
		r.runOnFinishHook()
		r.runMemoryCuration()
	}
	r.Log.Section("Cleanup")
	closed := false
	if s.InUserSession {
		if err := d.Workmux.Remove(r.ctx, d.Cwd, s.Handle); err != nil {
			r.Log.Warn("failed to close the session window: " + err.Error())
		} else {
			closed = true
			r.Log.Step("session window closed, worktree removed")
		}
	}
	if !closed {
		d.Git.RemoveWorktree(r.ctx, d.Cwd, s.WorktreePath)
		r.Log.Step("worktree removed, close the session's window when you are done")
		if s.Branch != "" {
			if err := d.Git.DeleteBranch(r.ctx, d.Cwd, s.Branch); err != nil {
				r.Log.Warn("failed to delete branch " + s.Branch + ": " + err.Error())
			}
		}
	}
	s.Status = session.Submitted
	if err := session.Save(d.Cwd, s); err != nil {
		return err
	}
	failure := ""
	if r.outcome != nil && r.outcome.Status == "failed" {
		failure = r.outcome.Error
	}
	r.Log.Finished(agent.Name, failure)
	return nil
}
