// Package skill symlinks the Claude Code skills in a checkout's skills/ directory into ~/.claude/skills.
package skill

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var ErrNoSkills = errors.New("no skills found. Skills should be directories under skills/ containing a SKILL.md file")

type Skill struct {
	Name   string
	Source string
}

func DefaultTarget() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "skills"), nil
}

func Discover(cwd string) []Skill {
	dir, err := filepath.Abs(filepath.Join(cwd, "skills"))
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var skills []Skill
	for _, e := range entries {
		source := filepath.Join(dir, e.Name())
		if _, err := os.Stat(filepath.Join(source, "SKILL.md")); e.IsDir() && err == nil {
			skills = append(skills, Skill{Name: e.Name(), Source: source})
		}
	}
	return skills
}

func Install(cwd, target string, out io.Writer) error {
	skills := Discover(cwd)
	if len(skills) == 0 {
		return ErrNoSkills
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	installed := 0
	for _, s := range skills {
		link := filepath.Join(target, s.Name)
		if info, err := os.Lstat(link); err == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				fmt.Fprintf(out, "  %s — skipped: %s exists and is not a symlink\n", s.Name, link)
				continue
			}
			existing, _ := os.Readlink(link)
			if existing == s.Source {
				fmt.Fprintf(out, "  %s — already installed\n", s.Name)
				continue
			}
			fmt.Fprintf(out, "  %s — updating (was linked to %s)\n", s.Name, existing)
			if err := os.Remove(link); err != nil {
				return err
			}
		}
		if err := os.Symlink(s.Source, link); err != nil {
			return err
		}
		fmt.Fprintf(out, "  %s — installed\n", s.Name)
		installed++
	}
	if installed > 0 {
		fmt.Fprintf(out, "\nInstalled %d skill(s). Use /<skill-name> in Claude Code from any project.\n", installed)
	} else {
		fmt.Fprintln(out, "\nAll skills already installed.")
	}
	return nil
}

func Uninstall(cwd, target string, out io.Writer) error {
	skills := Discover(cwd)
	if len(skills) == 0 {
		return ErrNoSkills
	}
	removed := 0
	for _, s := range skills {
		link := filepath.Join(target, s.Name)
		info, err := os.Lstat(link)
		if err != nil {
			fmt.Fprintf(out, "  %s — not installed\n", s.Name)
			continue
		}
		if info.Mode()&os.ModeSymlink == 0 {
			fmt.Fprintf(out, "  %s — skipped: %s is not a symlink\n", s.Name, link)
			continue
		}
		if err := os.Remove(link); err != nil {
			return err
		}
		fmt.Fprintf(out, "  %s — removed\n", s.Name)
		removed++
	}
	if removed > 0 {
		fmt.Fprintf(out, "\nRemoved %d skill(s). Source files in this repo are untouched.\n", removed)
	} else {
		fmt.Fprintln(out, "\nNo installed skills to remove.")
	}
	return nil
}
