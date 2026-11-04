package scaffold

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/config"
)

func initProject(t *testing.T) (string, string) {
	t.Helper()
	cwd := filepath.Join(t.TempDir(), "my-app")
	_ = os.Mkdir(cwd, 0o755)
	var out bytes.Buffer
	if err := Init(cwd, &out); err != nil {
		t.Fatal(err)
	}
	return cwd, out.String()
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestInitScaffoldsTheVteamDirectory(t *testing.T) {
	cwd, out := initProject(t)
	for _, agent := range DefaultAgents {
		for _, file := range []string{"agent.json", "AGENT.md", "MEMORY.md"} {
			if _, err := os.Stat(filepath.Join(cwd, "vteam", "agents", agent, file)); err != nil {
				t.Errorf("%s/%s: %v", agent, file, err)
			}
		}
		if _, err := config.ResolveAgent(agent, cwd); err != nil {
			t.Errorf("default agent %s does not load: %v", agent, err)
		}
	}
	if read(t, filepath.Join(cwd, "vteam", "agent.schema.json")) != string(config.AgentSchemaJSON) {
		t.Error("schema differs")
	}
	if !strings.Contains(out, "Created vteam/vteam.config.json (aven project: my-app)") {
		t.Errorf("output:\n%s", out)
	}
}

func TestInitWritesAValidConfigForTheDirectory(t *testing.T) {
	cwd, _ := initProject(t)
	cfg, err := config.Load(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tasks.Project != "my-app" || cfg.Tasks.Label != "vteam" || cfg.MergeEnsurer.Cron == "" {
		t.Errorf("config = %+v", cfg)
	}
	if !strings.HasSuffix(read(t, config.ConfigPath(cwd)), "}\n") {
		t.Error("config does not end with a newline")
	}
}

func TestInitScaffoldsTheSecurityAnalystAndRoutesItToTheCoder(t *testing.T) {
	cwd, _ := initProject(t)
	analyst, _ := config.ResolveAgent("security-analyst", cwd)
	if analyst.Output != "task" || analyst.Worktree || analyst.AutoPR {
		t.Errorf("security-analyst = %+v", analyst)
	}
	coder, _ := config.ResolveAgent("coder", cwd)
	foundBy := coder.TaskFilter["found-by"]
	for _, want := range []string{"improver", "security-analyst", "test-writer", ""} {
		found := false
		for _, v := range foundBy {
			found = found || v == want
		}
		if !found {
			t.Errorf("coder taskFilter %v misses %q", foundBy, want)
		}
	}
}

func TestInitCreatesGitignore(t *testing.T) {
	cwd, out := initProject(t)
	if diff := cmp.Diff(strings.Join(gitignoreEntries, "\n")+"\n", read(t, filepath.Join(cwd, ".gitignore"))); diff != "" {
		t.Error(diff)
	}
	if !strings.Contains(out, "Created .gitignore") {
		t.Errorf("output:\n%s", out)
	}
}

func TestInitAppendsToAnExistingGitignoreWithoutDuplicates(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "app")
	_ = os.Mkdir(cwd, 0o755)
	_ = os.WriteFile(filepath.Join(cwd, ".gitignore"), []byte("node_modules/\nvteam/.logs/\n"), 0o644)
	var out bytes.Buffer
	if err := Init(cwd, &out); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(cwd, ".gitignore"))
	if strings.Count(got, "vteam/.logs/") != 1 || !strings.HasPrefix(got, "node_modules/\n") || !strings.Contains(got, "vteam/.memory/") {
		t.Errorf(".gitignore = %q", got)
	}
	if !strings.Contains(out.String(), "Updated .gitignore with .vteam-worktrees/, vteam/.locks/, vteam/.runs/, vteam/.memory/") {
		t.Errorf("output:\n%s", out.String())
	}

	_ = os.RemoveAll(filepath.Join(cwd, "vteam"))
	out.Reset()
	_ = Init(cwd, &out)
	if strings.Contains(out.String(), ".gitignore") {
		t.Error("complete .gitignore was touched")
	}
}

func TestInitRefusesAnExistingVteamDirectory(t *testing.T) {
	cwd := t.TempDir()
	_ = os.Mkdir(filepath.Join(cwd, "vteam"), 0o755)
	if err := Init(cwd, &bytes.Buffer{}); !errors.Is(err, ErrExists) {
		t.Errorf("err = %v", err)
	}
}

func TestInitReportsWriteFailures(t *testing.T) {
	cwd := t.TempDir()
	_ = os.Mkdir(filepath.Join(cwd, ".gitignore"), 0o755)
	if err := Init(cwd, &bytes.Buffer{}); err == nil {
		t.Error("unreadable .gitignore accepted")
	}

	readOnly := t.TempDir()
	_ = os.Chmod(readOnly, 0o555)
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o755) })
	if err := Init(readOnly, &bytes.Buffer{}); err == nil {
		t.Error("read-only project accepted")
	}

	if _, err := Template("missing.md"); err == nil {
		t.Error("missing template found")
	}
}
