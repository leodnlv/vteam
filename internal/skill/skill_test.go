package skill

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func repoWithSkills(t *testing.T, names ...string) string {
	t.Helper()
	cwd := t.TempDir()
	for _, n := range names {
		_ = os.MkdirAll(filepath.Join(cwd, "skills", n), 0o755)
		_ = os.WriteFile(filepath.Join(cwd, "skills", n, "SKILL.md"), []byte("# "+n), 0o644)
	}
	return cwd
}

func link(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestDiscover(t *testing.T) {
	t.Run("returns nothing without a skills directory", func(t *testing.T) {
		if s := Discover(t.TempDir()); len(s) != 0 {
			t.Errorf("skills = %v", s)
		}
	})
	t.Run("ignores directories without SKILL.md and plain files", func(t *testing.T) {
		cwd := repoWithSkills(t, "real")
		_ = os.MkdirAll(filepath.Join(cwd, "skills", "empty"), 0o755)
		_ = os.WriteFile(filepath.Join(cwd, "skills", "SETUP.md"), []byte("x"), 0o644)
		want := []Skill{{Name: "real", Source: filepath.Join(cwd, "skills", "real")}}
		if diff := cmp.Diff(want, Discover(cwd)); diff != "" {
			t.Error(diff)
		}
	})
}

func TestInstall(t *testing.T) {
	t.Run("creates symlinks, creating the target directory", func(t *testing.T) {
		cwd := repoWithSkills(t, "vteam", "create-vteam-agent")
		target := filepath.Join(t.TempDir(), "nested", "skills")
		var out bytes.Buffer
		if err := Install(cwd, target, &out); err != nil {
			t.Fatal(err)
		}
		for _, n := range []string{"vteam", "create-vteam-agent"} {
			if got := link(t, filepath.Join(target, n)); got != filepath.Join(cwd, "skills", n) {
				t.Errorf("%s -> %s", n, got)
			}
		}
		if !strings.Contains(out.String(), "Installed 2 skill(s).") {
			t.Errorf("output:\n%s", out.String())
		}
	})

	t.Run("skips already-installed skills", func(t *testing.T) {
		cwd := repoWithSkills(t, "vteam")
		target := t.TempDir()
		_ = Install(cwd, target, &bytes.Buffer{})
		var out bytes.Buffer
		_ = Install(cwd, target, &out)
		if !strings.Contains(out.String(), "vteam — already installed") || !strings.Contains(out.String(), "All skills already installed.") {
			t.Errorf("output:\n%s", out.String())
		}
	})

	t.Run("updates symlinks pointing elsewhere", func(t *testing.T) {
		cwd := repoWithSkills(t, "vteam")
		target := t.TempDir()
		_ = os.Symlink("/somewhere/else", filepath.Join(target, "vteam"))
		var out bytes.Buffer
		_ = Install(cwd, target, &out)
		if link(t, filepath.Join(target, "vteam")) != filepath.Join(cwd, "skills", "vteam") || !strings.Contains(out.String(), "updating (was linked to /somewhere/else)") {
			t.Errorf("output:\n%s", out.String())
		}
	})

	t.Run("leaves non-symlink entries alone", func(t *testing.T) {
		cwd := repoWithSkills(t, "vteam")
		target := t.TempDir()
		_ = os.Mkdir(filepath.Join(target, "vteam"), 0o755)
		var out bytes.Buffer
		_ = Install(cwd, target, &out)
		if info, _ := os.Lstat(filepath.Join(target, "vteam")); info.Mode()&os.ModeSymlink != 0 || !strings.Contains(out.String(), "is not a symlink") {
			t.Errorf("output:\n%s", out.String())
		}
	})

	t.Run("fails when no skills are found", func(t *testing.T) {
		if err := Install(t.TempDir(), t.TempDir(), &bytes.Buffer{}); !errors.Is(err, ErrNoSkills) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestUninstall(t *testing.T) {
	t.Run("removes symlinks and reports missing ones", func(t *testing.T) {
		cwd := repoWithSkills(t, "vteam", "other")
		target := t.TempDir()
		_ = os.Symlink(filepath.Join(cwd, "skills", "vteam"), filepath.Join(target, "vteam"))
		var out bytes.Buffer
		if err := Uninstall(cwd, target, &out); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(target, "vteam")); !os.IsNotExist(err) {
			t.Error("symlink left")
		}
		for _, want := range []string{"vteam — removed", "other — not installed", "Removed 1 skill(s)."} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("missing %q in:\n%s", want, out.String())
			}
		}
		if _, err := os.Stat(filepath.Join(cwd, "skills", "vteam", "SKILL.md")); err != nil {
			t.Error("source removed")
		}
	})

	t.Run("skips non-symlink entries", func(t *testing.T) {
		cwd := repoWithSkills(t, "vteam")
		target := t.TempDir()
		_ = os.Mkdir(filepath.Join(target, "vteam"), 0o755)
		var out bytes.Buffer
		_ = Uninstall(cwd, target, &out)
		if !strings.Contains(out.String(), "is not a symlink") || !strings.Contains(out.String(), "No installed skills to remove.") {
			t.Errorf("output:\n%s", out.String())
		}
	})

	t.Run("fails when no skills are found", func(t *testing.T) {
		if err := Uninstall(t.TempDir(), t.TempDir(), &bytes.Buffer{}); !errors.Is(err, ErrNoSkills) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestDefaultTarget(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	if got, _ := DefaultTarget(); got != "/home/me/.claude/skills" {
		t.Errorf("got %q", got)
	}
}
