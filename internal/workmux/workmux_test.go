package workmux

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/execx"
)

func TestAddParsesTheHeadlessReceipt(t *testing.T) {
	f := &execx.Fake{Handler: func(execx.Cmd) (string, error) {
		return `{"schema_version":1,"handle":"vteam-fix-1","branch":"vteam/fix-1","worktree_path":"/w/vteam-fix-1","working_directory":"/w/vteam-fix-1","base_branch":null,"post_create_hooks_run":0}`, nil
	}}
	wt, err := (&CLI{Exec: f}).Add(context.Background(), "/repo", "vteam/fix-1")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(Worktree{Handle: "vteam-fix-1", Branch: "vteam/fix-1", Path: "/w/vteam-fix-1"}, wt); diff != "" {
		t.Error(diff)
	}
	if diff := cmp.Diff([][]string{{"workmux", "add", "vteam/fix-1", "--headless", "--json"}}, f.Args()); diff != "" {
		t.Error(diff)
	}
}

func TestAddRejectsAnEmptyReceipt(t *testing.T) {
	f := &execx.Fake{Handler: func(execx.Cmd) (string, error) { return `{}`, nil }}
	if _, err := (&CLI{Exec: f}).Add(context.Background(), "/repo", "b"); err == nil {
		t.Error("expected an error")
	}
}

func TestOpenPassesPromptConfigAndSession(t *testing.T) {
	f := &execx.Fake{}
	err := (&CLI{Exec: f}).Open(context.Background(), "/repo", OpenOptions{Handle: "h", PromptFile: "/p.md", ConfigFile: "/c.yaml", ParentSession: "vteam-app"})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"workmux", "open", "h", "--prompt-file", "/p.md", "--config", "/c.yaml", "--parent-session", "vteam-app"}}
	if diff := cmp.Diff(want, f.Args()); diff != "" {
		t.Error(diff)
	}
}

func TestListParsesWorktrees(t *testing.T) {
	f := &execx.Fake{Handler: func(execx.Cmd) (string, error) {
		return `[{"handle":"app","branch":"main","path":"/app","is_main":true,"is_open":false},{"handle":"h","branch":"b","path":"/w/h","is_open":true,"agent_statuses":["working"]}]`, nil
	}}
	list, err := (&CLI{Exec: f}).List(context.Background(), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	want := []Worktree{{Handle: "app", Branch: "main", Path: "/app"}, {Handle: "h", Branch: "b", Path: "/w/h", IsOpen: true}}
	if diff := cmp.Diff(want, list); diff != "" {
		t.Error(diff)
	}
}

func TestRemoveForcesByHandle(t *testing.T) {
	f := &execx.Fake{}
	if err := (&CLI{Exec: f}).Remove(context.Background(), "/repo", "h"); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([][]string{{"workmux", "remove", "--force", "h"}}, f.Args()); diff != "" {
		t.Error(diff)
	}
}

func TestAgentConfigQuotesTheCommand(t *testing.T) {
	got := AgentConfig(`claude --append-system-prompt-file '/a b/s.md' --model "sonnet"`)
	want := `agent: "claude --append-system-prompt-file '/a b/s.md' --model \"sonnet\""` + "\n"
	if got != want {
		t.Errorf("got %q", got)
	}
}
