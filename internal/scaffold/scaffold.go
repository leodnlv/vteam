// Package scaffold implements vteam init: it writes the default agents and config into a project.
package scaffold

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/leodnlv/vteam/internal/config"
)

//go:embed templates
var templates embed.FS

var DefaultAgents = []string{"improver", "security-analyst", "coder", "test-writer"}

var gitignoreEntries = []string{".vteam-worktrees/", "vteam/.locks/", "vteam/.logs/", "vteam/.runs/", "vteam/.memory/"}

var ErrExists = errors.New("vteam/ directory already exists. Aborting")

func Template(name string) ([]byte, error) {
	return templates.ReadFile("templates/" + name)
}

func copyTemplate(name, dest string) error {
	data, err := Template(name)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o644)
}

func indentJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(v)
	return buf.Bytes(), err
}

func Init(cwd string, out io.Writer) error {
	vteamDir := filepath.Join(cwd, "vteam")
	if _, err := os.Stat(vteamDir); err == nil {
		return ErrExists
	}
	fmt.Fprintln(out, "Scaffolding vteam/ directory...")

	for _, agent := range DefaultAgents {
		dir := filepath.Join(vteamDir, "agents", agent)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		for src, dest := range map[string]string{".agent.json": "agent.json", ".agent.md": "AGENT.md", ".memory.md": "MEMORY.md"} {
			if err := copyTemplate(agent+src, filepath.Join(dir, dest)); err != nil {
				return err
			}
		}
	}
	if err := os.WriteFile(filepath.Join(vteamDir, "agent.schema.json"), config.AgentSchemaJSON, 0o644); err != nil {
		return err
	}

	raw, err := Template("vteam.config.json")
	if err != nil {
		return err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	project := filepath.Base(cwd)
	cfg["tasks"].(map[string]any)["project"] = project
	data, err := indentJSON(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(config.ConfigPath(cwd), data, 0o644); err != nil {
		return err
	}

	if err := updateGitignore(cwd, out); err != nil {
		return err
	}
	for _, agent := range DefaultAgents {
		fmt.Fprintf(out, "  Created vteam/agents/%s/ (agent.json, AGENT.md, MEMORY.md)\n", agent)
	}
	fmt.Fprintln(out, "  Created vteam/agent.schema.json")
	fmt.Fprintf(out, "  Created vteam/vteam.config.json (aven project: %s)\n", project)
	fmt.Fprintln(out, "\nDone. Edit each agent's agent.json, AGENT.md, and MEMORY.md, and vteam.config.json, to customize.")
	return nil
}

func updateGitignore(cwd string, out io.Writer) error {
	path := filepath.Join(cwd, ".gitignore")
	existing, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, []byte(strings.Join(gitignoreEntries, "\n")+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(out, "  Created .gitignore")
		return nil
	}
	if err != nil {
		return err
	}
	var missing []string
	for _, e := range gitignoreEntries {
		if !strings.Contains(string(existing), e) {
			missing = append(missing, e)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	updated := string(existing) + "\n" + strings.Join(missing, "\n") + "\n"
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "  Updated .gitignore with %s\n", strings.Join(missing, ", "))
	return nil
}
