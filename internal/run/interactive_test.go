package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/output"
	"github.com/leodnlv/vteam/internal/session"
	"github.com/leodnlv/vteam/internal/tasks"
)

func (h *harness) runInteractive() error {
	h.t.Helper()
	agent, err := config.ResolveAgent("test-agent", h.cwd)
	if err != nil {
		h.t.Fatal(err)
	}
	return Run(context.Background(), h.deps(), agent, Options{Interactive: true, LogPath: "/logs/run.log"})
}

func (h *harness) mustHandOff() session.Session {
	h.t.Helper()
	if err := h.runInteractive(); err != nil {
		h.t.Fatalf("run failed: %v\n%s", err, h.out.String())
	}
	sessions := session.List(h.cwd)
	if len(sessions) != 1 {
		h.t.Fatalf("sessions = %+v", sessions)
	}
	return sessions[0]
}

func (h *harness) submit(s session.Session, doc string) (session.Session, error) {
	h.t.Helper()
	out, err := output.ParseInteractive(doc)
	if err != nil {
		h.t.Fatal(err)
	}
	agent, err := config.ResolveAgent("test-agent", h.cwd)
	if err != nil {
		h.t.Fatal(err)
	}
	return Submit(context.Background(), h.deps(), agent, s, out)
}

func (h *harness) finalize(s session.Session) {
	h.t.Helper()
	agent, err := config.ResolveAgent("test-agent", h.cwd)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := Finalize(context.Background(), h.deps(), agent, s); err != nil {
		h.t.Fatal(err)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

var worktreeTaskAgent = obj{"input": "task", "worktree": true, "autoPR": true}

func TestInteractiveHandOff(t *testing.T) {
	h := setup(t, project{agent: worktreeTaskAgent})
	seedTask(h, model.Task{})
	s := h.mustHandOff()

	if len(h.claude.Calls()) != 0 {
		t.Error("ran claude -p")
	}
	if !strings.HasPrefix(s.Branch, "vteam/fix-the-thing-") || s.PushBranch != s.Branch || s.RemoteSHA != "" {
		t.Errorf("branches = %q → %q (%q)", s.Branch, s.PushBranch, s.RemoteSHA)
	}
	if diff := cmp.Diff([]string{s.Branch + "<-main"}, h.git.branches); diff != "" {
		t.Error(diff)
	}
	if diff := cmp.Diff([]string{s.Branch}, h.workmux.added); diff != "" {
		t.Error(diff)
	}
	if s.Status != session.Open || s.Task.Ref != "VTM-ABCD" || s.LogPath != "/logs/run.log" || s.StartSHA == "" {
		t.Errorf("session = %+v", s)
	}

	if len(h.workmux.opened) != 1 {
		t.Fatalf("opened = %+v", h.workmux.opened)
	}
	opened := h.workmux.opened[0]
	if opened.Handle != s.Handle || opened.ParentSession != "vteam-test" {
		t.Errorf("opened = %+v", opened)
	}
	requireContains(t, readText(t, opened.PromptFile), "## Your Task", "It is broken.", "## Interactive Session",
		"vteam submit --project "+h.cwd+" --session "+s.RunID)
	requireNotContains(t, readText(t, opened.PromptFile), "Output ONLY valid JSON")
	requireContains(t, readText(t, opened.ConfigFile), `agent: "claude --append-system-prompt-file `+session.SystemPromptPath(h.cwd, s.RunID)+` --model sonnet"`)
	requireContains(t, readText(t, session.SystemPromptPath(h.cwd, s.RunID)), "You are a test agent.")

	claim := h.lastUpdate()
	if claim.Status != "active" || claim.Metadata["claimed-session"] != s.RunID || claim.Metadata["claimed-pid"] != "" {
		t.Errorf("claim = %+v", claim)
	}
	if len(h.tasks.Updates()) != 1 || len(h.git.removed) != 0 || len(h.workmux.removed) != 0 {
		t.Errorf("settled the hand-off: updates %+v removed %v %v", h.tasks.Updates(), h.git.removed, h.workmux.removed)
	}
	if _, err := os.Stat(filepath.Join(LocksDir(h.cwd), "test-agent.lock")); err == nil {
		t.Error("agent lock still held")
	}
	requireContains(t, h.out.String(), "session open in workmux window "+s.Handle, "tmux attach -t vteam-test")
}

func TestInteractiveHandOffInsideTmuxOpensInTheUsersSession(t *testing.T) {
	h := setup(t, project{agent: worktreeTaskAgent})
	seedTask(h, model.Task{})
	agent, err := config.ResolveAgent("test-agent", h.cwd)
	if err != nil {
		t.Fatal(err)
	}
	d := h.deps()
	d.InTmux = true
	if err := Run(context.Background(), d, agent, Options{Interactive: true}); err != nil {
		t.Fatal(err)
	}
	if len(h.workmux.opened) != 1 || h.workmux.opened[0].ParentSession != "" {
		t.Errorf("opened = %+v", h.workmux.opened)
	}
	requireNotContains(t, h.out.String(), "tmux attach")

	s := session.List(h.cwd)[0]
	if !s.InUserSession {
		t.Fatalf("session = %+v", s)
	}
	h.finalize(s)
	if diff := cmp.Diff([]string{s.Handle}, h.workmux.removed); diff != "" {
		t.Error(diff)
	}
	if len(h.git.removed) != 0 || len(h.git.deleted) != 0 {
		t.Errorf("removed by git too: %v %v", h.git.removed, h.git.deleted)
	}
	requireContains(t, h.out.String(), "session window closed, worktree removed")
}

func TestInteractiveFinalizeFallsBackToGitWhenTheWindowWontClose(t *testing.T) {
	h := setup(t, project{agent: worktreeTaskAgent})
	seedTask(h, model.Task{})
	s := h.mustHandOff()
	s.InUserSession = true
	h.workmux.removeErr = errors.New("no such window")
	h.finalize(s)
	if diff := cmp.Diff([]string{s.WorktreePath}, h.git.removed); diff != "" {
		t.Error(diff)
	}
	requireContains(t, h.out.String(), "failed to close the session window: no such window", "worktree removed, close the session's window")
}

func TestInteractiveSubmit(t *testing.T) {
	t.Run("commits, pushes, opens the PR, and settles the task; finalize closes the session", func(t *testing.T) {
		h := setup(t, project{agent: worktreeTaskAgent, onFinish: true})
		seedTask(h, model.Task{})
		s := h.mustHandOff()
		writeFile(t, filepath.Join(s.WorktreePath, "thing.ts"), "fixed")

		s, err := h.submit(s, jsonText(obj{"status": "completed", "summary": "Fixed it together.",
			"commitMessage": obj{"subject": "vteam: fix the thing", "body": "Paired fix."}}))
		if err != nil {
			t.Fatalf("submit: %v\n%s", err, h.out.String())
		}
		if len(h.git.pushes) != 1 || h.git.pushes[0].Branch != s.PushBranch {
			t.Errorf("pushes = %+v", h.git.pushes)
		}
		if len(h.platform.created) != 1 {
			t.Errorf("PRs = %+v", h.platform.created)
		}
		done := h.lastUpdate()
		if done.Status != "done" || done.Metadata["pr-url"] == "" {
			t.Errorf("done = %+v", done)
		}
		if diff := cmp.Diff(tasks.SessionClaimKeys, done.RemoveMetadata); diff != "" {
			t.Error(diff)
		}
		if s.Status != session.Submitting || s.Outcome == nil || s.Outcome.PRURL == "" {
			t.Errorf("session = %+v", s)
		}
		if len(h.claude.Calls()) != 0 || len(h.workmux.removed) != 0 {
			t.Error("finalized during submit")
		}

		h.finalize(s)
		if len(h.claude.Calls()) != 1 || !strings.Contains(h.prompt(0).UserPrompt, "PR URL: ") {
			t.Errorf("on-finish calls = %+v", h.claude.Calls())
		}
		if len(h.workmux.removed) != 0 {
			t.Errorf("closed the window: %v", h.workmux.removed)
		}
		if diff := cmp.Diff([]string{s.WorktreePath}, h.git.removed); diff != "" {
			t.Error(diff)
		}
		if diff := cmp.Diff([]string{s.Branch}, h.git.deleted); diff != "" {
			t.Error(diff)
		}
		requireContains(t, h.out.String(), "worktree removed, close the session's window when you are done")
		saved, _ := session.Load(h.cwd, s.RunID)
		if saved.Status != session.Submitted {
			t.Errorf("status = %s", saved.Status)
		}
	})

	t.Run("publishes commits made in the session without a commit message", func(t *testing.T) {
		h := setup(t, project{agent: worktreeTaskAgent})
		h.git.realSHA = true
		seedTask(h, model.Task{})
		s := h.mustHandOff()
		writeFile(t, filepath.Join(s.WorktreePath, "thing.ts"), "fixed")
		gitOutput(t, s.WorktreePath, "add", "-A")
		gitOutput(t, s.WorktreePath, "commit", "-q", "-m", "fix by hand")

		if _, err := h.submit(s, jsonText(obj{"status": "completed", "summary": "User committed."})); err != nil {
			t.Fatalf("submit: %v\n%s", err, h.out.String())
		}
		if len(h.git.pushes) != 1 || h.lastUpdate().Status != "done" {
			t.Errorf("pushes %+v last %+v", h.git.pushes, h.lastUpdate())
		}
		requireContains(t, h.out.String(), "pushing the commits made in the session")
	})

	t.Run("keeps the session open and the task claimed when the push fails", func(t *testing.T) {
		h := setup(t, project{agent: worktreeTaskAgent})
		h.git.realSHA = true
		seedTask(h, model.Task{})
		s := h.mustHandOff()
		writeFile(t, filepath.Join(s.WorktreePath, "thing.ts"), "fixed")
		h.git.pushErr = errors.New("stale info")

		_, err := h.submit(s, jsonText(obj{"status": "completed", "summary": "s", "commitMessage": obj{"subject": "vteam: x", "body": ""}}))
		if err == nil {
			t.Fatal("submit succeeded")
		}
		if len(h.tasks.Updates()) != 1 {
			t.Errorf("task updated: %+v", h.tasks.Updates())
		}
		saved, _ := session.Load(h.cwd, s.RunID)
		if saved.Status != session.Open {
			t.Errorf("status = %s", saved.Status)
		}

		h.git.pushErr = nil
		if _, err := h.submit(saved, jsonText(obj{"status": "completed", "summary": "s"})); err != nil {
			t.Fatalf("resubmit: %v", err)
		}
		if len(h.git.pushes) != 1 || h.lastUpdate().Status != "done" {
			t.Errorf("resubmit did not publish the earlier commit: %+v", h.git.pushes)
		}
	})

	t.Run("returns the task to todo with a retry when nothing changed", func(t *testing.T) {
		h := setup(t, project{agent: worktreeTaskAgent})
		seedTask(h, model.Task{})
		s := h.mustHandOff()
		if _, err := h.submit(s, jsonText(obj{"status": "blocked", "summary": "s", "blockerReason": "needs a decision"})); err != nil {
			t.Fatal(err)
		}
		last := h.lastUpdate()
		if last.Status != "todo" || last.Metadata["retry-count"] != "1" {
			t.Errorf("last = %+v", last)
		}
		requireContains(t, h.tasks.Notes()[0].Text, "test-agent: blocked", "Blocker: needs a decision")
	})
}

func TestInteractiveTaskFindings(t *testing.T) {
	h := setup(t, project{agent: obj{"output": "task"}})
	s := h.mustHandOff()
	if !s.Scratch || s.PushBranch != s.Branch {
		t.Errorf("session = %+v", s)
	}
	requireContains(t, readText(t, session.PromptPath(h.cwd, s.RunID)), `"type": "tasks"`)
	writeFile(t, filepath.Join(s.WorktreePath, "stray.ts"), "edit")

	finding := func(title string) obj {
		return obj{"title": title, "severity": "medium", "description": "d", "files": []string{"a.go"}}
	}
	s, err := h.submit(s, jsonText(obj{"status": "completed", "summary": "Two findings.",
		"content": obj{"type": "tasks", "body": []obj{finding("First"), finding("Second")}}}))
	if err != nil {
		t.Fatal(err)
	}
	created := h.tasks.Created()
	if len(created) != 2 || created[0].Body.Title != "First" || created[1].Body.Title != "Second" {
		t.Errorf("created = %+v", created)
	}
	if len(s.Outcome.TasksCreated) != 2 || len(h.git.pushes) != 0 {
		t.Errorf("outcome %+v pushes %+v", s.Outcome, h.git.pushes)
	}
	requireContains(t, h.out.String(), "agent without a worktree, nothing committed")
}

func TestInteractiveFollowUpPushesToThePRBranch(t *testing.T) {
	h := setup(t, project{agent: worktreeTaskAgent})
	seedDoneTaskReopened(h)
	mockPR(h, "open", "mergeable")
	h.platform.threads = threads
	s := h.mustHandOff()

	if s.Branch != "vteam-session/"+s.RunID || s.PushBranch != prBranch || s.RemoteSHA != "remote-"+prBranch+"-sha" {
		t.Errorf("session = %+v", s)
	}
	requireContains(t, readText(t, session.PromptPath(h.cwd, s.RunID)), "## Existing Pull Request", `"threadReplies"`)

	writeFile(t, filepath.Join(s.WorktreePath, "thing.ts"), "fixed")
	if _, err := h.submit(s, jsonText(obj{"status": "completed", "summary": "s", "commitMessage": obj{"subject": "vteam: x", "body": ""},
		"threadReplies": []obj{{"thread": "T1", "body": "Added the check."}}})); err != nil {
		t.Fatal(err)
	}
	if len(h.git.pushes) != 1 || h.git.pushes[0].Branch != prBranch || h.git.pushes[0].RemoteSHA != s.RemoteSHA {
		t.Errorf("pushes = %+v", h.git.pushes)
	}
	if len(h.platform.replies) != 1 || len(h.platform.created) != 0 {
		t.Errorf("replies %+v PRs %+v", h.platform.replies, h.platform.created)
	}
}

func TestInteractiveOpenFailureReleasesEverything(t *testing.T) {
	h := setup(t, project{agent: worktreeTaskAgent})
	seedTask(h, model.Task{})
	h.workmux.openErr = errors.New("no tmux")
	if err := h.runInteractive(); err == nil {
		t.Fatal("run succeeded")
	}
	if last := h.lastUpdate(); last.Status != "todo" {
		t.Errorf("last = %+v", last)
	}
	if diff := cmp.Diff(tasks.SessionClaimKeys, h.lastUpdate().RemoveMetadata); diff != "" {
		t.Error(diff)
	}
	if len(h.workmux.removed) != 1 || len(session.List(h.cwd)) != 0 {
		t.Errorf("removed %v sessions %+v", h.workmux.removed, session.List(h.cwd))
	}
}
