package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/leodnlv/vteam/internal/model"
)

const DefaultChangesRequestedLabel = "vteam:changes-requested"

type Tasks struct {
	MaxRetries int    `json:"maxRetries"`
	Project    string `json:"project"`
	Label      string `json:"label"`
	Workspace  string `json:"workspace,omitempty"`
}

type MergeEnsurer struct {
	Cron  string `json:"cron,omitempty"`
	Label string `json:"label"`
}

type Config struct {
	BaseBranch    string         `json:"baseBranch"`
	Platform      model.Platform `json:"platform"`
	WorktreeDir   string         `json:"worktreeDir"`
	Tasks         Tasks          `json:"tasks"`
	MergeEnsurer  MergeEnsurer   `json:"mergeEnsurer"`
	Notifications *bool          `json:"notifications,omitempty"`
}

// NotificationsEnabled is true unless vteam.config.json sets notifications to false.
func (c *Config) NotificationsEnabled() bool {
	return c.Notifications == nil || *c.Notifications
}

func str(minLen int) func(*checker, []string, any) {
	return func(c *checker, p []string, v any) { c.str(p, v, minLen) }
}

var tasksRules = []rule{
	{key: "maxRetries", requiredType: "number", check: func(c *checker, p []string, v any) { c.nonNegativeInt(p, v) }},
	{key: "project", requiredType: "string", check: str(1)},
	{key: "label", check: str(1)},
	{key: "workspace", check: str(1)},
}

var mergeEnsurerRules = []rule{
	{key: "cron", check: func(c *checker, p []string, v any) { c.cronExpr(p, v) }},
	{key: "label", check: str(1)},
}

var configRules = []rule{
	{key: "baseBranch", requiredType: "string", check: str(0)},
	{key: "platform", requiredType: "string", check: func(c *checker, p []string, v any) { c.enum(p, v, string(model.GitHub), string(model.GitLab)) }},
	{key: "worktreeDir", requiredType: "string", check: str(0)},
	{key: "tasks", requiredType: "object", check: func(c *checker, p []string, v any) { c.object(p, v, false, tasksRules) }},
	{key: "mergeEnsurer", check: func(c *checker, p []string, v any) { c.object(p, v, false, mergeEnsurerRules) }},
	{key: "notifications", check: func(c *checker, p []string, v any) { c.boolean(p, v) }},
}

// ParseConfig validates a decoded vteam.config.json and applies defaults. Unknown keys are ignored.
func ParseConfig(doc any) (*Config, []Issue) {
	c := &checker{}
	if !c.object(nil, doc, false, configRules) || len(c.issues) > 0 {
		return nil, c.issues
	}
	cfg := &Config{}
	if err := decodeChecked(doc, cfg); err != nil {
		return nil, []Issue{{Message: err.Error()}}
	}
	if cfg.Tasks.Label == "" {
		cfg.Tasks.Label = "vteam"
	}
	if cfg.MergeEnsurer.Label == "" {
		cfg.MergeEnsurer.Label = DefaultChangesRequestedLabel
	}
	return cfg, nil
}

func ConfigPath(cwd string) string {
	return filepath.Join(cwd, "vteam", "vteam.config.json")
}

func Load(cwd string) (*Config, error) {
	data, err := os.ReadFile(ConfigPath(cwd))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("vteam.config.json not found. Run 'vteam init' first")
	}
	if err != nil {
		return nil, err
	}
	doc, err := decodeDocument(data)
	if err != nil {
		return nil, fmt.Errorf("invalid JSON in vteam.config.json: %w", err)
	}
	cfg, issues := ParseConfig(doc)
	if len(issues) > 0 {
		return nil, fmt.Errorf("invalid vteam.config.json:\n%s", formatIssues(issues))
	}
	return cfg, nil
}
