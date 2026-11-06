// Package prompt assembles the layered prompts vteam sends to Claude.
package prompt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/model"
	"github.com/leodnlv/vteam/internal/output"
)

type Parts struct {
	SystemPrompt string `json:"systemPrompt"`
	UserPrompt   string `json:"userPrompt"`
	// OutputSchema is the JSON Schema for the agent's final answer; set only for agent runs, not hooks or curation.
	OutputSchema string `json:"-"`
}

type Input struct {
	Agent *config.Agent
	// Existing is every labelled task, listed so the agent does not report duplicates.
	Existing []model.Task
	Task     *model.Task
	Review   *model.PRReviewContext
	FollowUp *model.PRFollowUpContext
	Focus    string
	Memory   string
	// SubmitCommand makes the prompt an interactive one: Claude works with the user and submits its output by
	// piping it to this command, instead of returning it as its final answer.
	SubmitCommand string
}

func readPrompt(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func Build(in Input) (Parts, error) {
	system, err := readPrompt(in.Agent.AgentMDPath)
	if err != nil {
		return Parts{}, err
	}
	var sections []string
	if in.Focus != "" {
		sections = append(sections, "## Priority Focus\n\nThe user wants you to prioritize the following context above all other considerations:\n\n"+in.Focus)
	}
	if in.Memory != "" {
		sections = append(sections, "## Agent Memory\n\nThe following is your accumulated memory from previous runs. Use it to inform your work and avoid repeating past efforts.\n\n"+in.Memory)
	}
	if len(in.Existing) > 0 {
		lines := make([]string, len(in.Existing))
		for i, t := range in.Existing {
			lines[i] = fmt.Sprintf("- [%s] %s | %s", t.Status, t.Severity, t.Title)
		}
		sections = append(sections, "## Existing Tasks\n\nDo not report issues that already appear in this list.\n\n"+strings.Join(lines, "\n"))
	}
	if t := in.Task; t != nil {
		sections = append(sections, fmt.Sprintf("## Your Task\n\nTitle: %s\nSeverity: %s\n\n%s", t.Title, t.Severity, t.Description))
		if len(t.Notes) > 0 {
			sections = append(sections, taskNotesSection(t.Notes))
		}
	}
	switch {
	case in.FollowUp != nil:
		sections = append(sections, followUpSection(*in.FollowUp))
	case in.Review != nil:
		sections = append(sections, reviewSection(*in.Review))
	}
	replyable := in.FollowUp != nil && len(in.FollowUp.Threads) > 0
	if in.SubmitCommand != "" {
		sections = append(sections, interactiveOutputInstruction(in.Agent, replyable, in.SubmitCommand))
	} else {
		sections = append(sections, outputInstruction(in.Agent, replyable))
	}
	schema := output.Schema(output.SchemaOptions{
		TaskContent: in.Agent.Output == config.OutputTask, Memory: in.Agent.Memory != nil, ThreadReplies: replyable,
	})
	return Parts{SystemPrompt: system, UserPrompt: strings.Join(sections, "\n\n"), OutputSchema: schema}, nil
}

func OnFinish(cfg *config.Subagent, o model.RunOutcome) (Parts, error) {
	system, err := readPrompt(cfg.MDPath)
	if err != nil {
		return Parts{}, err
	}
	sections := []string{fmt.Sprintf("## Run Outcome\n\n- Agent: %s\n- Status: %s\n- Started: %s\n- Completed: %s", o.Agent, o.Status, o.StartedAt, o.CompletedAt)}
	if o.Focus != "" {
		sections = append(sections, "## Focus Context\n\n"+o.Focus)
	}
	if o.Task != nil {
		sections = append(sections, fmt.Sprintf("## Task\n\n- Title: %s\n- Severity: %s", o.Task.Title, o.Task.Severity))
	}
	if o.Branch != "" || o.PRURL != "" {
		var lines []string
		if o.Branch != "" {
			lines = append(lines, "- Branch: "+o.Branch)
		}
		if o.PRURL != "" {
			lines = append(lines, "- PR URL: "+o.PRURL)
		}
		sections = append(sections, "## Branch & PR\n\n"+strings.Join(lines, "\n"))
	}
	if pr := o.ReviewedPR; pr != nil {
		sections = append(sections, fmt.Sprintf("## Reviewed PR\n\n- Number: #%d\n- Title: %s\n- URL: %s", pr.Number, pr.Title, pr.URL))
	}
	if len(o.TasksCreated) > 0 {
		sections = append(sections, "## Tasks Created\n\n- "+strings.Join(o.TasksCreated, "\n- "))
	}
	if m := o.CommitMessage; m != nil {
		sections = append(sections, "## Commit Message\n\n"+m.Subject+"\n\n"+m.Body)
	}
	if c := o.Content; c != nil {
		body := c.Generic
		switch c.Type {
		case model.ContentTask:
			body = indentJSON(c.Task)
		case model.ContentTasks:
			body = indentJSON(c.Tasks)
		}
		sections = append(sections, "## Content\n\n"+body)
	}
	if o.Error != "" {
		sections = append(sections, "## Error\n\n"+o.Error)
	}
	return Parts{SystemPrompt: system, UserPrompt: strings.Join(sections, "\n\n")}, nil
}

func indentJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return strings.TrimSuffix(buf.String(), "\n")
}

func MemoryCuration(cfg *config.Subagent, current, update, focus string) (Parts, error) {
	system, err := readPrompt(cfg.MDPath)
	if err != nil {
		return Parts{}, err
	}
	var sections []string
	if focus != "" {
		sections = append(sections, "## Run Context\n\nThe agent was invoked with this focus:\n\n"+focus)
	}
	if current != "" {
		sections = append(sections, "## Current Memory\n\n"+current)
	} else {
		sections = append(sections, "## Current Memory\n\n(empty — this is the first memory entry)")
	}
	sections = append(sections,
		"## New Update\n\n"+update,
		"## Instructions\n\nReturn the complete replacement memory content. Merge the new update into the current memory according to your curation rules. Output ONLY the final memory content as plain text — no JSON, no fences.",
	)
	return Parts{SystemPrompt: system, UserPrompt: strings.Join(sections, "\n\n")}, nil
}

// ThreadRef is the label Claude uses for the index-th review thread, so it never handles platform IDs.
func ThreadRef(index int) string {
	return fmt.Sprintf("T%d", index+1)
}

const taskContentInstruction = `  "content": {
    "type": "task",
    "body": {
      "title": "short descriptive title",
      "severity": "critical|high|medium|low",
      "description": "detailed description of the issue and its impact",
      "suggestedFix": "how to fix it",
      "files": ["file:line"]
    }
  }`

const genericContentInstruction = `  "content": {
    "type": "generic",
    "body": "your primary output as a string (analysis, review, report, etc.)"
  }`

func outputInstruction(agent *config.Agent, replyableThreads bool) string {
	memoryNote, threadField, threadNote := "", "", ""
	if agent.Memory != nil {
		memoryNote = ",\n  \"memoryUpdate\": \"optional: brief notes about this run to remember for future runs (patterns noticed, decisions made, areas covered)\""
	}
	if replyableThreads {
		threadField = ",\n  \"threadReplies\": [{ \"thread\": \"T1\", \"body\": \"what you changed to address this thread\" }]"
		threadNote = "\n- \"threadReplies\" is optional. Add one entry per review thread you addressed in this change, using the thread's label (T1, T2, ...) from \"Open Review Threads\". Do not reply to threads you skipped. Do not post replies yourself; the orchestrator posts them after your change is pushed."
	}
	content := genericContentInstruction
	preamble := "After completing your work, output a JSON object as the LAST thing you produce. Do NOT run git add or git commit — the orchestrator handles committing and pushing. Output ONLY valid JSON with no markdown fencing."
	if agent.Output == config.OutputTask {
		content = taskContentInstruction
		preamble = "Return your finding as a JSON object. Do NOT create or modify tasks yourself (no aven commands) — the orchestrator creates them from your output. Output ONLY valid JSON with no markdown fencing."
	}
	return `## Output Format

` + preamble + `

{
  "status": "completed|partial|blocked|failed",
  "summary": "what you did",
` + content + `,
  "filesChanged": ["path/to/file1.ts"],
  "commitMessage": {
    "subject": "vteam: <short subject>",
    "body": "PR-ready description of the change"
  },
  "blockerReason": "only if status is blocked or failed"` + memoryNote + threadField + `
}

- "content" is optional. Include it when you have a primary deliverable (finding, review, analysis).
- "filesChanged" and "commitMessage" are optional. Include them only when you modified files.
- "blockerReason" is optional. Include it only when status is "blocked" or "failed".` + threadNote
}

const taskListContentInstruction = `  "content": {
    "type": "tasks",
    "body": [
      {
        "title": "short descriptive title",
        "severity": "critical|high|medium|low",
        "description": "detailed description of the issue and its impact",
        "suggestedFix": "how to fix it",
        "files": ["file:line"]
      }
    ]
  }`

// interactiveOutputInstruction replaces outputInstruction in an interactive session, where the output is
// submitted with a command once the user is done rather than returned as the final answer.
func interactiveOutputInstruction(agent *config.Agent, replyableThreads bool, submitCommand string) string {
	memoryField, threadField, threadNote := "", "", ""
	if agent.Memory != nil {
		memoryField = ",\n  \"memoryUpdate\": \"optional: brief notes about this session to remember for future runs (patterns noticed, decisions made, areas covered)\""
	}
	if replyableThreads {
		threadField = ",\n  \"threadReplies\": [{ \"thread\": \"T1\", \"body\": \"what you changed to address this thread\" }]"
		threadNote = "\n- \"threadReplies\" is optional. Add one entry per review thread you addressed, using the thread's label (T1, T2, ...) from \"Open Review Threads\". Do not reply to threads yourself; vteam posts the replies after the change is pushed."
	}
	content := genericContentInstruction
	work := "Do NOT run git commit or git push; vteam commits any uncommitted changes with your commit message and pushes. Commits the user makes themselves are pushed too."
	contentNote := "- \"content\" is optional. Include it when you have a primary deliverable (review, analysis, report)."
	if agent.Output == config.OutputTask {
		content = taskListContentInstruction
		work = "Do NOT create or modify tasks yourself (no aven commands); vteam creates one task per finding you submit. File changes are not kept."
		contentNote = "- \"content\" lists every finding the user agreed to report, one entry each. Use `\"type\": \"task\"` with a single object body only if there is exactly one."
	}
	return `## Interactive Session

This is an interactive session: the user is here with you. Work on the request above together with them, ask when something is unclear, and let them steer.

Do not submit on your own. When the user tells you they are done (or runs /vteam-submit), submit your result by piping a JSON object to this command:

` + "```" + `
` + submitCommand + ` <<'VTEAM_OUTPUT'
{ ...the JSON object below... }
VTEAM_OUTPUT
` + "```" + `

If the command reports an error, fix the JSON (or the problem it names) and run it again. When it succeeds, vteam closes this session. ` + work + `

{
  "status": "completed|partial|blocked|failed",
  "summary": "what you did",
` + content + `,
  "filesChanged": ["path/to/file1.ts"],
  "commitMessage": {
    "subject": "vteam: <short subject>",
    "body": "PR-ready description of the change"
  },
  "blockerReason": "only if status is blocked or failed"` + memoryField + threadField + `
}

` + contentNote + `
- "filesChanged" and "commitMessage" are optional. Include them only when files were modified.
- "blockerReason" is optional. Include it only when status is "blocked" or "failed".` + threadNote
}

func taskNotesSection(notes []model.TaskNote) string {
	entries := make([]string, len(notes))
	for i, n := range notes {
		entries[i] = "### " + n.CreatedAt + "\n\n" + n.Body
	}
	return "## Task Notes\n\nNotes left on this task by earlier runs and by humans, oldest first. Do not repeat an approach that already failed.\n\n" + strings.Join(entries, "\n\n")
}

func location(path string, line int) string {
	if line > 0 {
		return fmt.Sprintf("%s:%d", path, line)
	}
	return path
}

func reviewSection(r model.PRReviewContext) string {
	lines := []string{
		"## Pull Request", "",
		fmt.Sprintf("Number: #%d", r.PR.Number),
		"Repository: " + r.RepoSlug,
		"Title: " + r.PR.Title,
		"URL: " + r.PR.URL,
		"Branch: " + r.PR.Branch,
		"",
	}
	if len(r.Comments) > 0 {
		lines = append(lines, "## Review Comments", "", "Address each of the following review comments:", "")
		for _, c := range r.Comments {
			if c.Path != "" {
				lines = append(lines, fmt.Sprintf("### %s on `%s`", c.Author, location(c.Path, c.Line)))
			} else {
				lines = append(lines, "### "+c.Author)
			}
			lines = append(lines, "", c.Body, "")
		}
	}
	return strings.Join(lines, "\n")
}

func baseMergeSection(m model.BaseBranchMerge) string {
	if len(m.ConflictedFiles) == 0 {
		return strings.Join([]string{
			"## Merge Conflicts", "",
			fmt.Sprintf("The pull request conflicted with `%s`. The orchestrator merged `origin/%s` into this branch without conflicts, and will push the merge even if you change nothing else.", m.BaseBranch, m.BaseBranch),
		}, "\n")
	}
	lines := []string{
		"## Merge Conflicts", "",
		fmt.Sprintf("The pull request conflicts with `%s`. The orchestrator has started merging `origin/%s` into this branch, and these files contain conflict markers:", m.BaseBranch, m.BaseBranch),
		"",
	}
	for _, f := range m.ConflictedFiles {
		lines = append(lines, "- `"+f+"`")
	}
	lines = append(lines, "", "Resolve every conflict, keeping the intent of both sides, and remove all conflict markers. Do not run `git merge`, `git commit`, or `git merge --abort` — the orchestrator commits the merge with your commit message. If any conflict marker is left, nothing is committed.")
	return strings.Join(lines, "\n")
}

func followUpSection(f model.PRFollowUpContext) string {
	lines := []string{
		"## Existing Pull Request", "",
		"This task already has an open pull request. You are working on its branch, and your changes will be pushed to it. Build on the work that is already there instead of starting over.",
		"",
		"Title: " + f.PR.Title,
		"URL: " + f.PR.URL,
		"Branch: " + f.PR.Branch,
		"",
	}
	if f.BaseMerge != nil {
		lines = append(lines, baseMergeSection(*f.BaseMerge), "")
	}
	lines = append(lines, "## Open Review Threads", "")
	if len(f.Threads) == 0 {
		lines = append(lines, "The pull request has no unresolved review threads. Work from the task notes.")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "Unresolved review threads on the pull request. If the task notes say which of them to apply, apply only those. Otherwise address them all.", "")
	for i, th := range f.Threads {
		loc := "General"
		if th.Path != "" {
			loc = "`" + location(th.Path, th.Line) + "`"
		}
		lines = append(lines, fmt.Sprintf("### %s · %s", ThreadRef(i), loc), "")
		for _, c := range th.Comments {
			lines = append(lines, fmt.Sprintf("**%s:** %s", c.Author, c.Body), "")
		}
	}
	return strings.TrimRightFunc(strings.Join(lines, "\n"), unicode.IsSpace)
}
