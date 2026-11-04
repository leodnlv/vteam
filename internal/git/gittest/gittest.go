// Package gittest builds throwaway git repositories for tests.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func Run(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func configure(t testing.TB, dir string) {
	Run(t, dir, "config", "user.email", "test@test.com")
	Run(t, dir, "config", "user.name", "Test")
	Run(t, dir, "config", "commit.gpgsign", "false")
}

func WriteFile(t testing.TB, dir, file, content string) {
	t.Helper()
	path := filepath.Join(dir, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Commit writes file and commits it, returning the new HEAD.
func Commit(t testing.TB, dir, file, content string) string {
	t.Helper()
	WriteFile(t, dir, file, content)
	Run(t, dir, "add", "-A")
	Run(t, dir, "commit", "-q", "-m", file)
	return Run(t, dir, "rev-parse", "HEAD")
}

// Init creates a repository on main with one commit of file.txt.
func Init(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	Run(t, dir, "init", "-q", "-b", "main")
	configure(t, dir)
	Commit(t, dir, "file.txt", "init\n")
	return dir
}

// InitWithOrigin creates an origin repository with main and feature-branch, and a clone of it.
func InitWithOrigin(t testing.TB) (local, origin string) {
	t.Helper()
	origin = Init(t)
	Run(t, origin, "checkout", "-q", "-b", "feature-branch")
	Commit(t, origin, "feature.txt", "feature\n")
	Run(t, origin, "checkout", "-q", "main")

	local = filepath.Join(t.TempDir(), "local")
	Run(t, filepath.Dir(local), "clone", "-q", origin, local)
	configure(t, local)
	return local, origin
}

func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
