package prompt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/config"
	"github.com/leodnlv/vteam/internal/golden"
	"github.com/leodnlv/vteam/internal/model"
)

func writeMD(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type goldenAgent struct {
	Name     string
	Output   string
	Input    string
	Worktree bool
	Memory   bool
}

func TestBuildGolden(t *testing.T) {
	var cases []struct {
		Name          string
		Agent         goldenAgent
		AgentMd       string
		ExistingTasks []model.Task
		Task          *model.Task
		Review        json.RawMessage
		Focus         string
		MemoryContent string
		Expected      Parts
	}
	golden.Load(t, "prompt", &cases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			agent := &config.Agent{Name: c.Agent.Name, Output: c.Agent.Output, Input: c.Agent.Input, Worktree: c.Agent.Worktree,
				AgentMDPath: writeMD(t, "AGENT.md", c.AgentMd)}
			if c.Agent.Memory {
				agent.Memory = &config.Subagent{MDPath: "MEMORY.md"}
			}
			in := Input{Agent: agent, Existing: c.ExistingTasks, Task: c.Task, Focus: c.Focus, Memory: c.MemoryContent}
			if len(c.Review) > 0 {
				var probe map[string]json.RawMessage
				_ = json.Unmarshal(c.Review, &probe)
				if _, isFollowUp := probe["threads"]; isFollowUp {
					in.FollowUp = &model.PRFollowUpContext{}
					_ = json.Unmarshal(c.Review, in.FollowUp)
				} else {
					in.Review = &model.PRReviewContext{}
					_ = json.Unmarshal(c.Review, in.Review)
				}
			}
			got, err := Build(in)
			if err != nil {
				t.Fatal(err)
			}
			if got.OutputSchema == "" {
				t.Error("agent prompts must carry an output schema")
			}
			got.OutputSchema = ""
			if diff := cmp.Diff(c.Expected, got); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

func TestOnFinishGolden(t *testing.T) {
	var cases []struct {
		Name       string
		Outcome    model.RunOutcome
		OnFinishMd string
		Expected   Parts
	}
	golden.Load(t, "on-finish-prompt", &cases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := OnFinish(&config.Subagent{MDPath: writeMD(t, "ON_FINISH.md", c.OnFinishMd)}, c.Outcome)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(c.Expected, got); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

func TestMemoryCurationGolden(t *testing.T) {
	var cases []struct {
		Name, CurrentMemory, MemoryUpdate, Focus, MemoryMd string
		Expected                                           Parts
	}
	golden.Load(t, "memory-prompt", &cases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := MemoryCuration(&config.Subagent{MDPath: writeMD(t, "MEMORY.md", c.MemoryMd)}, c.CurrentMemory, c.MemoryUpdate, c.Focus)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(c.Expected, got); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

func build(t *testing.T, in Input) Parts {
	t.Helper()
	if in.Agent == nil {
		in.Agent = &config.Agent{Name: "bot"}
	}
	if in.Agent.AgentMDPath == "" {
		in.Agent.AgentMDPath = writeMD(t, "AGENT.md", "\n  You are a bot.  \n")
	}
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func requireOrder(t *testing.T, text string, parts ...string) {
	t.Helper()
	last := -1
	for _, p := range parts {
		i := strings.Index(text, p)
		if i == -1 {
			t.Fatalf("missing %q", p)
		}
		if i < last {
			t.Fatalf("%q is out of order", p)
		}
		last = i
	}
}

func TestBuild(t *testing.T) {
	t.Run("uses the trimmed AGENT.md as the system prompt", func(t *testing.T) {
		if p := build(t, Input{}); p.SystemPrompt != "You are a bot." {
			t.Errorf("system = %q", p.SystemPrompt)
		}
	})

	t.Run("orders focus, memory, existing tasks, task, notes, and output format", func(t *testing.T) {
		agent := &config.Agent{Name: "coder", Memory: &config.Subagent{}}
		task := &model.Task{Title: "Fix", Severity: "high", Description: "Body", Notes: []model.TaskNote{{Body: "first", CreatedAt: "t1"}, {Body: "second", CreatedAt: "t2"}}}
		p := build(t, Input{Agent: agent, Existing: []model.Task{{Status: "canceled", Severity: "low", Title: "Old"}}, Task: task, Focus: "auth", Memory: "remembered"})
		requireOrder(t, p.UserPrompt, "## Priority Focus", "auth", "## Agent Memory", "remembered",
			"## Existing Tasks", "- [canceled] low | Old", "## Your Task", "Title: Fix\nSeverity: high\n\nBody",
			"## Task Notes", "### t1\n\nfirst", "### t2\n\nsecond", "## Output Format", `"memoryUpdate"`)
	})

	t.Run("omits sections without content", func(t *testing.T) {
		p := build(t, Input{Task: &model.Task{Title: "x"}})
		for _, absent := range []string{"## Priority Focus", "## Agent Memory", "## Existing Tasks", "## Task Notes", "## Pull Request", "## Existing Pull Request", "memoryUpdate", "threadReplies"} {
			if strings.Contains(p.UserPrompt, absent) {
				t.Errorf("unexpected %q", absent)
			}
		}
	})

	t.Run("uses the task content schema only for output: task", func(t *testing.T) {
		task := build(t, Input{Agent: &config.Agent{Output: config.OutputTask}})
		generic := build(t, Input{})
		if !strings.Contains(task.UserPrompt, `"type": "task"`) || !strings.Contains(task.UserPrompt, "no aven commands") {
			t.Error("task schema missing")
		}
		if !strings.Contains(generic.UserPrompt, `"type": "generic"`) || !strings.Contains(generic.UserPrompt, "Do NOT run git add") {
			t.Error("generic schema missing")
		}
	})

	t.Run("labels follow-up threads and offers threadReplies only when threads are open", func(t *testing.T) {
		pr := model.PullRequestInfo{ReviewablePR: model.ReviewablePR{Title: "PR", URL: "u", Branch: "b"}}
		threads := []model.ReviewThread{
			{ID: "X1", Path: "a.ts", Line: 3, Comments: []model.ReviewComment{{Author: "amy", Body: "rename"}}},
			{ID: "X2", Comments: []model.ReviewComment{{Author: "bob", Body: "general"}}},
		}
		p := build(t, Input{FollowUp: &model.PRFollowUpContext{PR: pr, Threads: threads}})
		requireOrder(t, p.UserPrompt, "## Existing Pull Request", "### T1 · `a.ts:3`", "**amy:** rename", "### T2 · General", "**bob:** general", `"threadReplies"`)
		if strings.Contains(p.UserPrompt, "X1") {
			t.Error("platform thread ID leaked into the prompt")
		}
		empty := build(t, Input{FollowUp: &model.PRFollowUpContext{PR: pr}})
		if !strings.Contains(empty.UserPrompt, "no unresolved review threads") || strings.Contains(empty.UserPrompt, "threadReplies") {
			t.Error("empty follow-up rendered wrong")
		}
	})

	t.Run("fails when AGENT.md cannot be read", func(t *testing.T) {
		if _, err := Build(Input{Agent: &config.Agent{AgentMDPath: filepath.Join(t.TempDir(), "missing.md")}}); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestOnFinishRendersTaskContentAsJSON(t *testing.T) {
	cfg := &config.Subagent{MDPath: writeMD(t, "ON_FINISH.md", "Notify.")}
	p, err := OnFinish(cfg, model.RunOutcome{Agent: "a", Status: "completed", Content: &model.Content{Type: model.ContentTask,
		Task: &model.TaskContentBody{Title: "<b>", Severity: "low", Description: "a & b", Files: []string{"x.ts"}}}})
	if err != nil {
		t.Fatal(err)
	}
	want := "## Content\n\n{\n  \"title\": \"<b>\",\n  \"severity\": \"low\",\n  \"description\": \"a & b\",\n  \"files\": [\n    \"x.ts\"\n  ]\n}"
	if !strings.HasSuffix(p.UserPrompt, want) {
		t.Errorf("prompt:\n%s", p.UserPrompt)
	}
}

func TestMemoryCurationPlacesRunContextFirst(t *testing.T) {
	cfg := &config.Subagent{MDPath: writeMD(t, "MEMORY.md", "Curate.")}
	p, _ := MemoryCuration(cfg, "old", "new", "auth")
	requireOrder(t, p.UserPrompt, "## Run Context", "## Current Memory\n\nold", "## New Update\n\nnew", "## Instructions")
	p, _ = MemoryCuration(cfg, "", "new", "")
	if strings.Contains(p.UserPrompt, "## Run Context") || !strings.Contains(p.UserPrompt, "(empty — this is the first memory entry)") {
		t.Errorf("prompt:\n%s", p.UserPrompt)
	}
}

func TestThreadRef(t *testing.T) {
	if ThreadRef(0) != "T1" || ThreadRef(9) != "T10" {
		t.Error("wrong labels")
	}
}
