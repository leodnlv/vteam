package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/leodnlv/vteam/internal/model"
)

func TestSaveLoadAndList(t *testing.T) {
	cwd := t.TempDir()
	newer := Session{RunID: "run-2", Agent: "coder", Status: Open, StartedAt: "2026-11-06T12:00:00.000Z", WorktreePath: "/w/b",
		Task: &model.Task{Ref: "APP-1"}, FollowUp: &model.PRFollowUpContext{Threads: []model.ReviewThread{{ID: "T"}}}}
	older := Session{RunID: "run-1", Agent: "improver", Status: Submitted, StartedAt: "2026-11-06T11:00:00.000Z", WorktreePath: "/w/a"}
	for _, s := range []Session{newer, older} {
		if err := Save(cwd, s); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Load(cwd, "run-2")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(newer, got); diff != "" {
		t.Error(diff)
	}
	if list := List(cwd); len(list) != 2 || list[0].RunID != "run-1" {
		t.Errorf("list = %+v", list)
	}
	if open := ListOpen(cwd); len(open) != 1 || open[0].RunID != "run-2" {
		t.Errorf("open = %+v", open)
	}
	if diff := cmp.Diff([]string{"/w/b"}, WorktreePaths(cwd)); diff != "" {
		t.Error(diff)
	}
	if data, _ := os.ReadFile(filepath.Join(Root(cwd), ".gitignore")); string(data) != "*\n" {
		t.Errorf(".gitignore = %q", data)
	}
	if _, err := Load(cwd, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestFindByDir(t *testing.T) {
	cwd, wt := t.TempDir(), t.TempDir()
	if err := Save(cwd, Session{RunID: "run-1", WorktreePath: wt}); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(wt, "pkg")
	_ = os.MkdirAll(sub, 0o755)
	for _, dir := range []string{wt, sub} {
		if s, err := FindByDir(cwd, dir); err != nil || s.RunID != "run-1" {
			t.Errorf("%s: %+v %v", dir, s, err)
		}
	}
	if _, err := FindByDir(cwd, wt+"-other"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestProjectDirFindsTheMainCheckoutFromAWorktree(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	main := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	git(main, "init", "-q", "-b", "main")
	_ = os.MkdirAll(filepath.Join(main, "vteam"), 0o755)
	git(main, "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	git(main, "worktree", "add", "-q", "-b", "b", wt)

	got, err := ProjectDir(context.Background(), wt)
	if err != nil {
		t.Fatal(err)
	}
	if real, _ := filepath.EvalSymlinks(main); canonical(got) != real {
		t.Errorf("got %s, want %s", got, main)
	}
	if _, err := ProjectDir(context.Background(), t.TempDir()); err == nil {
		t.Error("expected an error outside a repository")
	}
}
