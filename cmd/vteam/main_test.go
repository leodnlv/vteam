package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/leodnlv/vteam/internal/cli"
	"github.com/rogpeppe/go-internal/testscript"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"vteam":   func() { os.Exit(cli.Main()) },
		"claude":  fakeClaude,
		"gh":      fakeGH,
		"workmux": fakeWorkmux,
	})
}

func TestScripts(t *testing.T) {
	realAven, _ := exec.LookPath("aven")
	testscript.Run(t, testscript.Params{
		Dir: "testdata/script",
		Setup: func(e *testscript.Env) error {
			bin := filepath.Join(e.WorkDir, ".bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				return err
			}
			wrapper := fmt.Sprintf("#!/bin/sh\nexec %q --db %q \"$@\"\n", realAven, filepath.Join(e.WorkDir, ".aven.sqlite"))
			if err := os.WriteFile(filepath.Join(bin, "aven"), []byte(wrapper), 0o755); err != nil {
				return err
			}
			e.Setenv("PATH", bin+string(os.PathListSeparator)+e.Getenv("PATH"))
			e.Setenv("HOME", filepath.Join(e.WorkDir, ".home"))
			e.Setenv("NO_COLOR", "1")
			e.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
			e.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
			e.Setenv("GIT_AUTHOR_NAME", "Test")
			e.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
			e.Setenv("GIT_COMMITTER_NAME", "Test")
			e.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
			e.Setenv("FAKE_DIR", filepath.Join(e.WorkDir, "fake"))
			return nil
		},
		Condition: func(cond string) (bool, error) {
			if cond == "aven" {
				return realAven != "", nil
			}
			return false, fmt.Errorf("unknown condition %q", cond)
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"triage": triage,
		},
	})
}

// triage moves every inbox task to todo, like a human approving findings.
func triage(ts *testscript.TestScript, neg bool, args []string) {
	if err := ts.Exec("aven", "list", "--project", args[0], "--status", "inbox", "--json"); err != nil {
		ts.Fatalf("aven list: %v", err)
	}
	stdout := ts.ReadFile("stdout")
	var tasks []struct{ Ref string }
	if err := json.Unmarshal([]byte(stdout), &tasks); err != nil {
		ts.Fatalf("parse aven list: %v\n%s", err, stdout)
	}
	if len(tasks) == 0 {
		ts.Fatalf("no inbox tasks")
	}
	for _, t := range tasks {
		ts.Check(ts.Exec("aven", "edit", t.Ref, "--status", "todo"))
	}
}

func nextCall(dir string) int {
	path := filepath.Join(dir, "calls")
	data, _ := os.ReadFile(path)
	n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	n++
	_ = os.WriteFile(path, []byte(strconv.Itoa(n)), 0o644)
	return n
}

// fakeClaude answers the n-th call with $FAKE_DIR/claude/<n>.json (or default.json), after running <n>.sh
// in its working directory. Prompts are appended to $FAKE_DIR/claude/prompts.log.
func fakeClaude() {
	dir := filepath.Join(os.Getenv("FAKE_DIR"), "claude")
	_ = os.MkdirAll(dir, 0o755)
	prompt, _ := io.ReadAll(os.Stdin)
	n := nextCall(dir)
	logFile, _ := os.OpenFile(filepath.Join(dir, "prompts.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	fmt.Fprintf(logFile, "=== call %d: %s\n%s\n", n, strings.Join(os.Args[1:], " "), prompt)
	_ = logFile.Close()

	if script := filepath.Join(dir, fmt.Sprintf("%d.sh", n)); fileExists(script) {
		cmd := exec.Command("sh", script)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "fake claude script failed:", err)
			os.Exit(1)
		}
	}
	reply, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%d.json", n)))
	if err != nil {
		reply, err = os.ReadFile(filepath.Join(dir, "default.json"))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake claude has no reply for call", n)
		os.Exit(2)
	}
	if exit := strings.TrimSpace(string(reply)); strings.HasPrefix(exit, "exit ") {
		code, _ := strconv.Atoi(strings.TrimPrefix(exit, "exit "))
		os.Exit(code)
	}
	emit := func(v any) {
		data, _ := json.Marshal(v)
		fmt.Println(string(data))
	}
	emit(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{
		map[string]any{"type": "text", "text": "Working on it."},
		map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"file_path": "README.md"}},
	}}})
	emit(map[string]any{"type": "result", "result": strings.TrimSpace(string(reply)), "total_cost_usd": 0.01, "duration_ms": 1000, "num_turns": 2})
}

// fakeGH logs every call to $FAKE_DIR/gh.log and answers pr create and pr view from $FAKE_DIR/gh-*.
func fakeGH() {
	dir := os.Getenv("FAKE_DIR")
	_ = os.MkdirAll(dir, 0o755)
	logFile, _ := os.OpenFile(filepath.Join(dir, "gh.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	fmt.Fprintln(logFile, strings.Join(os.Args[1:], " "))
	_ = logFile.Close()
	if len(os.Args) < 3 {
		return
	}
	switch os.Args[1] + " " + os.Args[2] {
	case "pr create":
		fmt.Println("https://github.com/org/repo/pull/1")
	case "pr view":
		data, err := os.ReadFile(filepath.Join(dir, "gh-pr-view.json"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "no such PR")
			os.Exit(1)
		}
		os.Stdout.Write(data)
	case "label create":
		fmt.Fprintln(os.Stderr, "label already exists")
		os.Exit(1)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// fakeWorkmux makes real git worktrees under $WORK/wm, logs every call to $FAKE_DIR/workmux.log, and links the
// last worktree to $WORK/session so scripts can cd into it. A window counts as open until $FAKE_DIR/closed/<handle>
// exists.
func fakeWorkmux() {
	dir := os.Getenv("FAKE_DIR")
	state := filepath.Join(dir, "workmux")
	_ = os.MkdirAll(state, 0o755)
	logFile, _ := os.OpenFile(filepath.Join(dir, "workmux.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	fmt.Fprintln(logFile, strings.Join(os.Args[1:], " "))
	_ = logFile.Close()
	work := filepath.Dir(dir)
	fail := func(err error, out []byte) {
		fmt.Fprintln(os.Stderr, err, string(out))
		os.Exit(1)
	}
	switch os.Args[1] {
	case "add":
		branch := os.Args[2]
		handle := strings.ReplaceAll(branch, "/", "-")
		path := filepath.Join(work, "wm", handle)
		if out, err := exec.Command("git", "worktree", "add", "-q", path, branch).CombinedOutput(); err != nil {
			fail(err, out)
		}
		_ = os.WriteFile(filepath.Join(state, handle), []byte(path+"\n"+branch), 0o644)
		_ = os.Remove(filepath.Join(work, "session"))
		_ = os.Symlink(path, filepath.Join(work, "session"))
		data, _ := json.Marshal(map[string]any{"handle": handle, "branch": branch, "worktree_path": path})
		fmt.Println(string(data))
	case "open":
		prompt := ""
		for i, a := range os.Args {
			if a == "--prompt-file" {
				prompt = os.Args[i+1]
			}
		}
		data, _ := os.ReadFile(prompt)
		_ = os.WriteFile(filepath.Join(dir, "session-prompt.md"), data, 0o644)
	case "list":
		entries, _ := os.ReadDir(state)
		list := []map[string]any{}
		for _, e := range entries {
			data, _ := os.ReadFile(filepath.Join(state, e.Name()))
			path, branch, _ := strings.Cut(string(data), "\n")
			list = append(list, map[string]any{"handle": e.Name(), "branch": branch, "path": path,
				"is_open": !fileExists(filepath.Join(dir, "closed", e.Name()))})
		}
		data, _ := json.Marshal(list)
		fmt.Println(string(data))
	case "remove":
		handle := os.Args[len(os.Args)-1]
		data, err := os.ReadFile(filepath.Join(state, handle))
		if err != nil {
			fail(err, nil)
		}
		path, branch, _ := strings.Cut(string(data), "\n")
		main := filepath.Dir(filepath.Dir(path))
		if out, err := exec.Command("git", "-C", filepath.Join(main, "app"), "worktree", "remove", "--force", path).CombinedOutput(); err != nil {
			fail(err, out)
		}
		_ = exec.Command("git", "-C", filepath.Join(main, "app"), "branch", "-D", branch).Run()
		_ = os.Remove(filepath.Join(state, handle))
	}
}
