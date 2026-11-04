package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// TaskFilter maps aven metadata keys to accepted values. Keys AND, values OR.
// The empty string stands for JSON null: the key is missing or empty on the task.
type TaskFilter map[string][]string

type Subagent struct {
	MDPath          string
	Model           string   `json:"model"`
	AllowedTools    []string `json:"allowedTools"`
	DisallowedTools []string `json:"disallowedTools"`
}

type Agent struct {
	Name            string
	AgentMDPath     string
	Model           string     `json:"model"`
	Cron            string     `json:"cron"`
	Worktree        bool       `json:"worktree"`
	ReadOnly        bool       `json:"readOnly"`
	Output          string     `json:"output"`
	Input           string     `json:"input"`
	TaskFilter      TaskFilter `json:"-"`
	PRTriggerLabel  string     `json:"prTriggerLabel"`
	AutoPR          bool       `json:"autoPR"`
	PRCreateLabels  []string   `json:"prCreateLabels"`
	ResolveThreads  bool       `json:"resolveThreads"`
	AllowedTools    []string   `json:"allowedTools"`
	DisallowedTools []string   `json:"disallowedTools"`
	Memory          *Subagent  `json:"memory"`
	OnFinish        *Subagent  `json:"onFinish"`
}

const (
	OutputTask = "task"
	InputTask  = "task"
	InputPR    = "pr"
)

var subagentRules = []rule{
	{key: "model", check: func(c *checker, p []string, v any) { c.str(p, v, 0) }},
	{key: "allowedTools", check: func(c *checker, p []string, v any) { c.strings(p, v) }},
	{key: "disallowedTools", check: func(c *checker, p []string, v any) { c.strings(p, v) }},
}

var agentRules = []rule{
	{key: "$schema", check: func(c *checker, p []string, v any) { c.str(p, v, 0) }},
	{key: "model", check: func(c *checker, p []string, v any) { c.str(p, v, 0) }},
	{key: "cron", check: func(c *checker, p []string, v any) { c.cronExpr(p, v) }},
	{key: "worktree", check: func(c *checker, p []string, v any) { c.boolean(p, v) }},
	{key: "readOnly", check: func(c *checker, p []string, v any) { c.boolean(p, v) }},
	{key: "output", check: func(c *checker, p []string, v any) { c.enum(p, v, OutputTask) }},
	{key: "input", check: func(c *checker, p []string, v any) { c.enum(p, v, InputTask, InputPR) }},
	{key: "taskFilter", check: checkTaskFilter},
	{key: "prTriggerLabel", check: func(c *checker, p []string, v any) { c.str(p, v, 0) }},
	{key: "autoPR", check: func(c *checker, p []string, v any) { c.boolean(p, v) }},
	{key: "prCreateLabels", check: func(c *checker, p []string, v any) { c.strings(p, v) }},
	{key: "resolveThreads", check: func(c *checker, p []string, v any) { c.boolean(p, v) }},
	{key: "allowedTools", check: func(c *checker, p []string, v any) { c.strings(p, v) }},
	{key: "disallowedTools", check: func(c *checker, p []string, v any) { c.strings(p, v) }},
	{key: "memory", check: func(c *checker, p []string, v any) { c.object(p, v, true, subagentRules) }},
	{key: "onFinish", check: func(c *checker, p []string, v any) { c.object(p, v, true, subagentRules) }},
}

func filterValueOK(v any) bool {
	s, isStr := v.(string)
	return v == nil || (isStr && s != "")
}

func checkTaskFilter(c *checker, path []string, v any) {
	if !c.expect(path, v, "object") {
		return
	}
	obj := v.(map[string]any)
	for _, key := range sortedKeys(obj) {
		if key == "" {
			c.add(path, "Invalid key in record")
			continue
		}
		value := obj[key]
		switch value := value.(type) {
		case []any:
			if len(value) == 0 {
				c.add(at(path, key), "Too small: expected array to have >=1 items")
			} else if !slices.ContainsFunc(value, func(item any) bool { return !filterValueOK(item) }) {
				continue
			} else {
				c.add(at(path, key), "Invalid input")
			}
		case string:
			if value == "" {
				c.add(at(path, key), "Too small: expected string to have >=1 characters")
			}
		case nil:
		default:
			c.add(at(path, key), "Invalid input")
		}
	}
}

func toTaskFilter(v any) TaskFilter {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	filter := TaskFilter{}
	for key, value := range obj {
		items, isList := value.([]any)
		if !isList {
			items = []any{value}
		}
		for _, item := range items {
			s, _ := item.(string)
			filter[key] = append(filter[key], s)
		}
	}
	return filter
}

func (a *Agent) refinements() []string {
	var errs []string
	if a.Input == InputPR && !a.Worktree {
		errs = append(errs, `input: "pr" requires worktree: true`)
	}
	if a.ReadOnly && !a.Worktree {
		errs = append(errs, "readOnly: true requires worktree: true")
	}
	if a.ReadOnly && a.AutoPR {
		errs = append(errs, "readOnly: true is incompatible with autoPR: true")
	}
	if a.Output == OutputTask && a.AutoPR {
		errs = append(errs, `output: "task" is incompatible with autoPR: true`)
	}
	if a.Output == OutputTask && a.ReadOnly {
		errs = append(errs, `output: "task" is incompatible with readOnly: true`)
	}
	if a.TaskFilter != nil && a.Input != InputTask {
		errs = append(errs, `taskFilter requires input: "task"`)
	}
	if a.ResolveThreads && (a.Input != InputTask || !a.Worktree) {
		errs = append(errs, `resolveThreads requires input: "task" and worktree: true`)
	}
	return errs
}

// ParseAgentConfig validates the decoded agent.json document. Refinements run only once the shape is valid.
func ParseAgentConfig(doc any) (*Agent, []Issue) {
	c := &checker{}
	if !c.object(nil, doc, true, agentRules) || len(c.issues) > 0 {
		return nil, c.issues
	}
	agent := &Agent{}
	if err := decodeChecked(doc, agent); err != nil {
		return nil, []Issue{{Message: err.Error()}}
	}
	agent.TaskFilter = toTaskFilter(doc.(map[string]any)["taskFilter"])
	for _, msg := range agent.refinements() {
		c.issues = append(c.issues, Issue{Message: msg})
	}
	if len(c.issues) > 0 {
		return nil, c.issues
	}
	return agent, nil
}

func formatIssues(issues []Issue) string {
	lines := make([]string, len(issues))
	for i, issue := range issues {
		path := strings.Join(issue.Path, ".")
		if path == "" {
			path = "(root)"
		}
		lines[i] = "  - " + path + ": " + issue.Message
	}
	return strings.Join(lines, "\n")
}

func assertNoFrontmatter(path, label string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.HasPrefix(strings.TrimPrefix(string(raw), "\uFEFF"), "---") {
		return fmt.Errorf("%s starts with YAML frontmatter, which is no longer supported. Move its fields to agent.json and leave only the prompt in the markdown file", label)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func AgentsDir(cwd string) string {
	return filepath.Join(cwd, "vteam", "agents")
}

func ResolveAgent(name, cwd string) (*Agent, error) {
	dir := filepath.Join(AgentsDir(cwd), name)
	agentMD := filepath.Join(dir, "AGENT.md")
	if !fileExists(agentMD) {
		return nil, fmt.Errorf("agent %q not found at %s", name, agentMD)
	}
	if err := assertNoFrontmatter(agentMD, name+"/AGENT.md"); err != nil {
		return nil, err
	}

	var doc any = map[string]any{}
	if data, err := os.ReadFile(filepath.Join(dir, "agent.json")); err == nil {
		if doc, err = decodeDocument(data); err != nil {
			return nil, fmt.Errorf("invalid JSON in %s/agent.json: %w", name, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	agent, issues := ParseAgentConfig(doc)
	if len(issues) > 0 {
		return nil, fmt.Errorf("invalid config in %s/agent.json:\n%s", name, formatIssues(issues))
	}
	agent.Name = name
	agent.AgentMDPath = agentMD

	var err error
	if agent.OnFinish, err = attachSubagent(dir, name, "ON_FINISH.md", "onFinish", agent.OnFinish); err != nil {
		return nil, err
	}
	if agent.Memory, err = attachSubagent(dir, name, "MEMORY.md", "memory", agent.Memory); err != nil {
		return nil, err
	}
	return agent, nil
}

func attachSubagent(dir, name, file, section string, cfg *Subagent) (*Subagent, error) {
	path := filepath.Join(dir, file)
	if !fileExists(path) {
		if cfg != nil {
			return nil, fmt.Errorf("%s/agent.json has a %q section but %s/%s does not exist", name, section, name, file)
		}
		return nil, nil
	}
	if err := assertNoFrontmatter(path, name+"/"+file); err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = &Subagent{}
	}
	cfg.MDPath = path
	return cfg, nil
}

// ListAgentNames returns the agent directories under vteam/agents that contain an AGENT.md, sorted.
func ListAgentNames(cwd string) []string {
	entries, err := os.ReadDir(AgentsDir(cwd))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && fileExists(filepath.Join(AgentsDir(cwd), e.Name(), "AGENT.md")) {
			names = append(names, e.Name())
		}
	}
	return names
}
