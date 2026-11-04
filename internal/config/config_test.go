package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/golden"
	"github.com/leodnlv/vteam/internal/model"
)

func messages(issues []Issue) []string {
	out := []string{}
	for _, i := range issues {
		out = append(out, i.Message)
	}
	return out
}

func TestAgentConfigGolden(t *testing.T) {
	var cases []struct {
		Input  map[string]any
		OK     bool
		Errors []string
	}
	golden.Load(t, "agent-config", &cases)
	for _, c := range cases {
		input, _ := json.Marshal(c.Input)
		t.Run(string(input), func(t *testing.T) {
			_, issues := ParseAgentConfig(c.Input)
			if diff := cmp.Diff(c.Errors, messages(issues)); diff != "" {
				t.Errorf("errors (-want +got):\n%s", diff)
			}
		})
	}
}

func TestVteamConfigGolden(t *testing.T) {
	var cases []struct {
		Input    map[string]any
		OK       bool
		Expected *Config
	}
	golden.Load(t, "vteam-config", &cases)
	for _, c := range cases {
		input, _ := json.Marshal(c.Input)
		t.Run(string(input), func(t *testing.T) {
			cfg, issues := ParseConfig(c.Input)
			if !c.OK {
				if len(issues) == 0 {
					t.Fatalf("ParseConfig succeeded: %+v", cfg)
				}
				return
			}
			if len(issues) > 0 {
				t.Fatalf("issues: %v", issues)
			}
			if diff := cmp.Diff(c.Expected, cfg); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

type obj = map[string]any

func validAgent(t *testing.T, doc obj) *Agent {
	t.Helper()
	agent, issues := ParseAgentConfig(doc)
	if len(issues) > 0 {
		t.Fatalf("expected valid, got %v", issues)
	}
	return agent
}

func invalidAgent(t *testing.T, doc obj, fragment string) {
	t.Helper()
	_, issues := ParseAgentConfig(doc)
	if len(issues) == 0 {
		t.Fatal("expected invalid")
	}
	if joined := strings.Join(messages(issues), "; "); !strings.Contains(joined, fragment) {
		t.Errorf("issues %q do not mention %q", joined, fragment)
	}
}

func TestAgentConfigSchema(t *testing.T) {
	t.Run("accepts taskFilter with strings, lists and null", func(t *testing.T) {
		agent := validAgent(t, obj{"input": "task", "taskFilter": obj{"found-by": []any{"improver", nil}, "actor": nil, "team": "core"}})
		want := TaskFilter{"found-by": {"improver", ""}, "actor": {""}, "team": {"core"}}
		if diff := cmp.Diff(want, agent.TaskFilter); diff != "" {
			t.Error(diff)
		}
	})
	t.Run("rejects taskFilter without input: task", func(t *testing.T) {
		invalidAgent(t, obj{"taskFilter": obj{"actor": "coder"}}, `taskFilter requires input: "task"`)
	})
	t.Run("accepts resolveThreads on a worktree task agent", func(t *testing.T) {
		validAgent(t, obj{"input": "task", "worktree": true, "resolveThreads": true})
	})
	t.Run("rejects resolveThreads without input: task and worktree", func(t *testing.T) {
		invalidAgent(t, obj{"input": "task", "resolveThreads": true}, "resolveThreads requires")
	})
	t.Run("rejects non-string taskFilter values and empty lists", func(t *testing.T) {
		invalidAgent(t, obj{"input": "task", "taskFilter": obj{"actor": 1}}, "Invalid input")
		invalidAgent(t, obj{"input": "task", "taskFilter": obj{"actor": []any{}}}, "Too small")
		invalidAgent(t, obj{"input": "task", "taskFilter": obj{"actor": []any{"x", 2}}}, "Invalid input")
	})
	t.Run("rejects unknown keys", func(t *testing.T) {
		invalidAgent(t, obj{"modle": "sonnet"}, `Unrecognized key: "modle"`)
	})
	t.Run("rejects null for optional fields", func(t *testing.T) {
		invalidAgent(t, obj{"model": nil}, "expected string, received null")
	})
	t.Run("accepts $schema, memory and onFinish sections", func(t *testing.T) {
		agent := validAgent(t, obj{"$schema": "../../agent.schema.json", "memory": obj{"model": "haiku"},
			"onFinish": obj{"allowedTools": []any{"Read"}, "disallowedTools": []any{"Bash"}}})
		if agent.Memory.Model != "haiku" || agent.OnFinish.AllowedTools[0] != "Read" {
			t.Errorf("got %+v", agent)
		}
	})
	t.Run("accepts empty config (bare AGENT.md)", func(t *testing.T) {
		validAgent(t, obj{})
	})
	t.Run("accepts full config", func(t *testing.T) {
		agent := validAgent(t, obj{"model": "sonnet", "cron": "0 */6 * * *", "worktree": true, "input": "task",
			"autoPR": true, "prCreateLabels": []any{"vteam"}, "prTriggerLabel": "fix-me",
			"allowedTools": []any{"Read", "Bash(git *)"}, "disallowedTools": []any{"WebFetch"}})
		want := &Agent{Model: "sonnet", Cron: "0 */6 * * *", Worktree: true, Input: "task", AutoPR: true,
			PRCreateLabels: []string{"vteam"}, PRTriggerLabel: "fix-me",
			AllowedTools: []string{"Read", "Bash(git *)"}, DisallowedTools: []string{"WebFetch"}}
		if diff := cmp.Diff(want, agent); diff != "" {
			t.Error(diff)
		}
	})
	t.Run("rejects invalid input value", func(t *testing.T) {
		invalidAgent(t, obj{"input": "issue"}, `Invalid option: expected one of "task"|"pr"`)
	})
	t.Run("rejects invalid output value", func(t *testing.T) {
		invalidAgent(t, obj{"output": "generic"}, `Invalid input: expected "task"`)
	})
	t.Run("rejects invalid cron pattern", func(t *testing.T) {
		invalidAgent(t, obj{"cron": "every hour"}, "Invalid cron expression")
	})
	t.Run("rejects a non-boolean worktree", func(t *testing.T) {
		invalidAgent(t, obj{"worktree": "yes"}, "Invalid input: expected boolean, received string")
	})
}

func TestVteamConfigSchema(t *testing.T) {
	base := func() obj {
		return obj{"baseBranch": "main", "platform": "github", "worktreeDir": ".w", "tasks": obj{"maxRetries": 3.0, "project": "app"}}
	}
	t.Run("defaults label to vteam and the merge ensurer label", func(t *testing.T) {
		cfg, issues := ParseConfig(base())
		if len(issues) > 0 {
			t.Fatal(issues)
		}
		if cfg.Tasks.Label != "vteam" || cfg.MergeEnsurer != (MergeEnsurer{Label: DefaultChangesRequestedLabel}) {
			t.Errorf("got %+v", cfg)
		}
	})
	t.Run("accepts optional workspace", func(t *testing.T) {
		doc := base()
		doc["tasks"].(obj)["workspace"] = "work"
		cfg, issues := ParseConfig(doc)
		if len(issues) > 0 || cfg.Tasks.Workspace != "work" {
			t.Errorf("cfg %+v issues %v", cfg, issues)
		}
	})
	for name, mutate := range map[string]func(obj){
		"rejects unknown platform":              func(d obj) { d["platform"] = "bitbucket" },
		"rejects missing baseBranch":            func(d obj) { delete(d, "baseBranch") },
		"rejects negative maxRetries":           func(d obj) { d["tasks"].(obj)["maxRetries"] = -1.0 },
		"rejects fractional maxRetries":         func(d obj) { d["tasks"].(obj)["maxRetries"] = 1.5 },
		"rejects missing project":               func(d obj) { delete(d["tasks"].(obj), "project") },
		"rejects empty project":                 func(d obj) { d["tasks"].(obj)["project"] = "" },
		"rejects an invalid merge ensurer cron": func(d obj) { d["mergeEnsurer"] = obj{"cron": "every minute"} },
	} {
		t.Run(name, func(t *testing.T) {
			doc := base()
			mutate(doc)
			if _, issues := ParseConfig(doc); len(issues) == 0 {
				t.Error("expected issues")
			}
		})
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupAgent(t *testing.T) (cwd, dir string) {
	cwd = t.TempDir()
	dir = filepath.Join(cwd, "vteam", "agents", "bot")
	writeFile(t, filepath.Join(dir, "AGENT.md"), "You are a bot.")
	return cwd, dir
}

func writeAgentJSON(t *testing.T, dir string, v any) {
	data, _ := json.Marshal(v)
	writeFile(t, filepath.Join(dir, "agent.json"), string(data))
}

func requireErrorContaining(t *testing.T, err error, fragment string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), fragment) {
		t.Fatalf("error = %v, want it to contain %q", err, fragment)
	}
}

func TestResolveAgent(t *testing.T) {
	t.Run("uses defaults when agent.json is missing", func(t *testing.T) {
		cwd, dir := setupAgent(t)
		agent, err := ResolveAgent("bot", cwd)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(&Agent{Name: "bot", AgentMDPath: filepath.Join(dir, "AGENT.md")}, agent); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("reads agent settings from agent.json and ignores $schema", func(t *testing.T) {
		cwd, dir := setupAgent(t)
		writeAgentJSON(t, dir, obj{"$schema": "../../agent.schema.json", "model": "sonnet", "worktree": true, "input": "task"})
		agent, err := ResolveAgent("bot", cwd)
		if err != nil {
			t.Fatal(err)
		}
		if agent.Model != "sonnet" || !agent.Worktree || agent.Input != "task" {
			t.Errorf("got %+v", agent)
		}
	})

	t.Run("attaches memory and onFinish config to their markdown files", func(t *testing.T) {
		cwd, dir := setupAgent(t)
		writeAgentJSON(t, dir, obj{"memory": obj{"model": "haiku"}, "onFinish": obj{"allowedTools": []string{"Bash(curl *)"}}})
		writeFile(t, filepath.Join(dir, "MEMORY.md"), "Curate.")
		writeFile(t, filepath.Join(dir, "ON_FINISH.md"), "Notify.")
		agent, err := ResolveAgent("bot", cwd)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(&Subagent{MDPath: filepath.Join(dir, "MEMORY.md"), Model: "haiku"}, agent.Memory); diff != "" {
			t.Error(diff)
		}
		if diff := cmp.Diff(&Subagent{MDPath: filepath.Join(dir, "ON_FINISH.md"), AllowedTools: []string{"Bash(curl *)"}}, agent.OnFinish); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("enables memory and onFinish from markdown files alone", func(t *testing.T) {
		cwd, dir := setupAgent(t)
		writeFile(t, filepath.Join(dir, "MEMORY.md"), "Curate.")
		writeFile(t, filepath.Join(dir, "ON_FINISH.md"), "Notify.")
		agent, err := ResolveAgent("bot", cwd)
		if err != nil {
			t.Fatal(err)
		}
		if agent.Memory.MDPath == "" || agent.OnFinish.MDPath == "" {
			t.Errorf("got %+v", agent)
		}
	})

	t.Run("rejects a memory section without MEMORY.md", func(t *testing.T) {
		cwd, dir := setupAgent(t)
		writeAgentJSON(t, dir, obj{"memory": obj{"model": "haiku"}})
		_, err := ResolveAgent("bot", cwd)
		requireErrorContaining(t, err, `"memory" section but bot/MEMORY.md does not exist`)
	})

	t.Run("rejects an onFinish section without ON_FINISH.md", func(t *testing.T) {
		cwd, dir := setupAgent(t)
		writeAgentJSON(t, dir, obj{"onFinish": obj{"model": "haiku"}})
		_, err := ResolveAgent("bot", cwd)
		requireErrorContaining(t, err, `"onFinish" section but bot/ON_FINISH.md does not exist`)
	})

	t.Run("rejects malformed JSON", func(t *testing.T) {
		cwd, dir := setupAgent(t)
		writeFile(t, filepath.Join(dir, "agent.json"), "{ model: sonnet }")
		_, err := ResolveAgent("bot", cwd)
		requireErrorContaining(t, err, "invalid JSON in bot/agent.json")
	})

	t.Run("rejects unknown keys with their path", func(t *testing.T) {
		cwd, dir := setupAgent(t)
		writeAgentJSON(t, dir, obj{"modle": "sonnet", "memory": obj{"modle": "haiku"}})
		writeFile(t, filepath.Join(dir, "MEMORY.md"), "Curate.")
		_, err := ResolveAgent("bot", cwd)
		requireErrorContaining(t, err, "invalid config in bot/agent.json:\n  - memory: Unrecognized key: \"modle\"\n  - (root): Unrecognized key: \"modle\"")
	})

	t.Run("rejects cross-field rule violations", func(t *testing.T) {
		cwd, dir := setupAgent(t)
		writeAgentJSON(t, dir, obj{"input": "pr"})
		_, err := ResolveAgent("bot", cwd)
		requireErrorContaining(t, err, `input: "pr" requires worktree: true`)
	})

	for _, file := range []string{"AGENT.md", "MEMORY.md", "ON_FINISH.md"} {
		t.Run("rejects leftover frontmatter in "+file, func(t *testing.T) {
			cwd, dir := setupAgent(t)
			writeFile(t, filepath.Join(dir, file), "---\nmodel: sonnet\n---\n\nPrompt.")
			_, err := ResolveAgent("bot", cwd)
			requireErrorContaining(t, err, "bot/"+file+" starts with YAML frontmatter")
		})
	}

	t.Run("rejects frontmatter behind a byte order mark", func(t *testing.T) {
		cwd, dir := setupAgent(t)
		writeFile(t, filepath.Join(dir, "AGENT.md"), "\uFEFF---\nmodel: x\n---\n")
		_, err := ResolveAgent("bot", cwd)
		requireErrorContaining(t, err, "starts with YAML frontmatter")
	})

	t.Run("reports a missing agent", func(t *testing.T) {
		_, err := ResolveAgent("ghost", t.TempDir())
		requireErrorContaining(t, err, `agent "ghost" not found`)
	})
}

func TestListAgentNames(t *testing.T) {
	cwd, _ := setupAgent(t)
	writeFile(t, filepath.Join(cwd, "vteam", "agents", "alpha", "AGENT.md"), "x")
	writeFile(t, filepath.Join(cwd, "vteam", "agents", "no-prompt", "agent.json"), "{}")
	writeFile(t, filepath.Join(cwd, "vteam", "agents", "stray.md"), "x")
	if diff := cmp.Diff([]string{"alpha", "bot"}, ListAgentNames(cwd)); diff != "" {
		t.Error(diff)
	}
	if names := ListAgentNames(t.TempDir()); names != nil {
		t.Errorf("no agents dir = %v", names)
	}
}

func writeConfig(t *testing.T, cwd string, v any) {
	data, _ := json.MarshalIndent(v, "", "  ")
	writeFile(t, ConfigPath(cwd), string(data))
}

func TestLoad(t *testing.T) {
	valid := obj{"baseBranch": "main", "platform": "github", "worktreeDir": ".vteam-worktrees", "tasks": obj{"maxRetries": 3, "project": "app"}}

	t.Run("throws when vteam.config.json does not exist", func(t *testing.T) {
		_, err := Load(t.TempDir())
		requireErrorContaining(t, err, "vteam.config.json not found")
	})

	t.Run("parses a valid config and returns typed data", func(t *testing.T) {
		cwd := t.TempDir()
		writeConfig(t, cwd, valid)
		cfg, err := Load(cwd)
		if err != nil {
			t.Fatal(err)
		}
		want := &Config{BaseBranch: "main", Platform: model.GitHub, WorktreeDir: ".vteam-worktrees",
			Tasks:        Tasks{MaxRetries: 3, Project: "app", Label: "vteam"},
			MergeEnsurer: MergeEnsurer{Label: DefaultChangesRequestedLabel}}
		if diff := cmp.Diff(want, cfg); diff != "" {
			t.Error(diff)
		}
	})

	t.Run("rejects an invalid merge ensurer cron", func(t *testing.T) {
		cwd := t.TempDir()
		doc := obj{}
		for k, v := range valid {
			doc[k] = v
		}
		doc["mergeEnsurer"] = obj{"cron": "every minute"}
		writeConfig(t, cwd, doc)
		_, err := Load(cwd)
		requireErrorContaining(t, err, "mergeEnsurer.cron")
	})

	t.Run("throws with field path on invalid platform", func(t *testing.T) {
		cwd := t.TempDir()
		writeConfig(t, cwd, obj{"baseBranch": "main", "platform": "bitbucket", "worktreeDir": ".w", "tasks": obj{"maxRetries": 3, "project": "app"}})
		_, err := Load(cwd)
		requireErrorContaining(t, err, "  - platform: Invalid option")
	})

	t.Run("throws on invalid JSON", func(t *testing.T) {
		cwd := t.TempDir()
		writeFile(t, ConfigPath(cwd), "not json{{{")
		_, err := Load(cwd)
		requireErrorContaining(t, err, "invalid JSON in vteam.config.json")
	})

	t.Run("reports multiple errors at once", func(t *testing.T) {
		cwd := t.TempDir()
		writeConfig(t, cwd, obj{"worktreeDir": ".w", "tasks": obj{"maxRetries": 3, "project": "app"}})
		_, err := Load(cwd)
		requireErrorContaining(t, err, "invalid vteam.config.json:\n  - baseBranch: Invalid input: expected string, received undefined\n  - platform:")
	})
}

func schemaKeys(t *testing.T, node map[string]any) []string {
	t.Helper()
	props, ok := node["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema node has no properties")
	}
	keys := sortedKeys(props)
	return keys
}

func ruleKeys(rules []rule) []string {
	var keys []string
	for _, r := range rules {
		keys = append(keys, r.key)
	}
	slices.Sort(keys)
	return keys
}

func TestAgentSchemaMatchesValidator(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(AgentSchemaJSON, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["additionalProperties"] != false {
		t.Error("root schema must reject additional properties")
	}
	if diff := cmp.Diff(ruleKeys(agentRules), schemaKeys(t, schema)); diff != "" {
		t.Errorf("agent.json keys (-validator +schema):\n%s", diff)
	}
	props := schema["properties"].(map[string]any)
	for _, section := range []string{"memory", "onFinish"} {
		node := props[section].(map[string]any)
		if node["additionalProperties"] != false {
			t.Errorf("%s must reject additional properties", section)
		}
		if diff := cmp.Diff(ruleKeys(subagentRules), schemaKeys(t, node)); diff != "" {
			t.Errorf("%s keys (-validator +schema):\n%s", section, diff)
		}
	}
}

func TestDogfoodSchemaIsCurrent(t *testing.T) {
	committed, err := os.ReadFile(filepath.Join(golden.RepoRoot(t), "vteam", "agent.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(committed) != string(AgentSchemaJSON) {
		t.Error("vteam/agent.schema.json differs from internal/config/agent.schema.json; copy it over")
	}
}

func TestValidatorTypeErrors(t *testing.T) {
	for name, doc := range map[string]any{
		"not an object":            []any{1},
		"tools not a list":         obj{"allowedTools": "Read"},
		"tool not a string":        obj{"allowedTools": []any{1}},
		"taskFilter not an object": obj{"input": "task", "taskFilter": []any{}},
		"memory not an object":     obj{"memory": "haiku"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, issues := ParseAgentConfig(doc); len(issues) == 0 {
				t.Error("expected issues")
			}
		})
	}
	base := obj{"baseBranch": "main", "platform": "github", "worktreeDir": ".w", "tasks": obj{"maxRetries": "3", "project": "p"}}
	if _, issues := ParseConfig(base); len(issues) == 0 || issues[0].Message != "Invalid input: expected number, received string" {
		t.Errorf("issues = %v", issues)
	}
	if _, issues := ParseConfig("nope"); len(issues) == 0 {
		t.Error("string config accepted")
	}
}

func TestLoadReportsUnreadableFiles(t *testing.T) {
	cwd := t.TempDir()
	_ = os.MkdirAll(ConfigPath(cwd), 0o755)
	if _, err := Load(cwd); err == nil {
		t.Error("directory accepted as config")
	}
	cwd, dir := setupAgent(t)
	_ = os.Mkdir(filepath.Join(dir, "agent.json"), 0o755)
	if _, err := ResolveAgent("bot", cwd); err == nil {
		t.Error("directory accepted as agent.json")
	}
}
