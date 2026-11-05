package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSetAgentCronKeepsKeyOrderAndFormatting(t *testing.T) {
	cwd := t.TempDir()
	dir := filepath.Join(AgentsDir(cwd), "coder")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("code"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("keep notes"), 0o644)
	original := "{\n  \"$schema\": \"../../agent.schema.json\",\n  \"model\": \"sonnet\",\n  \"cron\": \"*/5 * * * *\",\n  \"prCreateLabels\": [\n    \"vteam\"\n  ],\n  \"memory\": {\n    \"model\": \"haiku\"\n  }\n}\n"
	_ = os.WriteFile(filepath.Join(dir, "agent.json"), []byte(original), 0o644)

	if err := SetAgentCron(cwd, "coder", "0 * * * *"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "agent.json"))
	want := "{\n  \"$schema\": \"../../agent.schema.json\",\n  \"model\": \"sonnet\",\n  \"cron\": \"0 * * * *\",\n  \"prCreateLabels\": [\n    \"vteam\"\n  ],\n  \"memory\": {\n    \"model\": \"haiku\"\n  }\n}\n"
	if diff := cmp.Diff(want, string(got)); diff != "" {
		t.Error(diff)
	}
	if agent, err := ResolveAgent("coder", cwd); err != nil || agent.Cron != "0 * * * *" {
		t.Errorf("agent = %+v, err = %v", agent, err)
	}
}

func TestSetAgentCronCreatesAgentJSON(t *testing.T) {
	cwd := t.TempDir()
	dir := filepath.Join(AgentsDir(cwd), "plain")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("x"), 0o644)
	if err := SetAgentCron(cwd, "plain", "0 * * * *"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "agent.json"))
	if diff := cmp.Diff("{\n  \"$schema\": \"../../agent.schema.json\",\n  \"cron\": \"0 * * * *\"\n}\n", string(got)); diff != "" {
		t.Error(diff)
	}
	if err := SetAgentCron(cwd, "ghost", "0 * * * *"); err == nil {
		t.Error("unknown agent accepted")
	}
}

func TestSetMergeEnsurerCronAddsTheSection(t *testing.T) {
	cwd := t.TempDir()
	_ = os.MkdirAll(filepath.Dir(ConfigPath(cwd)), 0o755)
	_ = os.WriteFile(ConfigPath(cwd), []byte(`{"baseBranch":"main","platform":"github","worktreeDir":".wt","tasks":{"project":"p","maxRetries":3}}`), 0o644)
	if err := SetMergeEnsurerCron(cwd, "*/30 * * * *"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cwd)
	if err != nil || cfg.MergeEnsurer.Cron != "*/30 * * * *" || cfg.Tasks.Project != "p" {
		t.Fatalf("cfg = %+v, err = %v", cfg, err)
	}
	if err := SetMergeEnsurerCron(cwd, "0 * * * *"); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := Load(cwd); cfg.MergeEnsurer.Cron != "0 * * * *" {
		t.Errorf("cron = %q", cfg.MergeEnsurer.Cron)
	}
}
